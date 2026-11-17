package sendspin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	handshakeTimeout = 30 * time.Second

	activateTimeout = 30 * time.Second

	idleTimeout = 30 * time.Second

	rolePlayer = "player@v1"

	roleMetadata = "metadata@v1"

	roleController = "controller@v1"
	actPlayback    = "playback"
	actPairing     = "pairing"
	methodPSK      = "pairing_psk"
	maxReadBytes   = 4 << 20
)

type session struct {
	c      *Client
	ws     *websocket.Conn
	remote string

	sendMu sync.Mutex
	tr     *transport

	quiet  bool
	closed bool

	filter *timeFilter

	h            []byte
	serverID     string
	ra           reassembler
	activated    bool
	activities   map[string]bool
	pairingIndex int
	stagedPSK    []byte
	awaitHello   bool
	dec          *decoder
	streaming    bool

	mu         sync.Mutex
	serverName string
	psk        resolvedPSK
	roles      []string
	stateSent  bool
	clockNoted bool

	serverPlaying bool

	serverSaid bool

	asked atomic.Value

	settleGen atomic.Int64
	group     groupUpdate
	lastAudio time.Time

	metadata streamMetadata

	controller *controllerState
}

func newSession(c *Client, ws *websocket.Conn) *session {
	return &session{c: c, ws: ws, remote: ws.RemoteAddr().String(), filter: newTimeFilter()}
}

var errProtocol = errors.New("sendspin: protocol error")

func (s *session) run() {
	defer s.close()
	s.ws.SetReadLimit(maxReadBytes)
	if err := s.handshake(); err != nil {
		log.Printf("[sendspin] %s: handshake failed: %v", s.remote, err)
		return
	}
	log.Printf("[sendspin] %s: encrypted session with %q (%s)", s.remote, s.name(), s.psk.cat)

	s.ws.SetReadDeadline(time.Now().Add(handshakeTimeout))
	typ, body, err := s.readJSON()
	if err != nil || typ != "server/hello" {
		log.Printf("[sendspin] %s: expected server/hello, got %q (%v)", s.remote, typ, err)
		return
	}
	var sh serverHello
	json.Unmarshal(body, &sh)
	if sh.Name != "" {
		s.mu.Lock()
		s.serverName = sh.Name
		s.mu.Unlock()
	}
	if err := s.send("client/hello", s.c.hello()); err != nil {
		return
	}

	s.ws.SetReadDeadline(time.Now().Add(activateTimeout))
	stopSync := make(chan struct{})
	defer close(stopSync)
	for {
		msg, err := s.readMsg()
		if err != nil {
			if !errors.Is(err, errGoodbye) {
				log.Printf("[sendspin] %s: %v", s.remote, err)
			}
			return
		}
		if msg == nil {
			continue
		}
		if s.activated {
			s.ws.SetReadDeadline(time.Now().Add(idleTimeout))
		}
		if msg[0] == msgAudioChunk {
			s.onAudio(msg)
			continue
		}
		if msg[0] != msgJSON {
			continue
		}
		var env envelope
		if err := json.Unmarshal(msg[1:], &env); err != nil {
			log.Printf("[sendspin] %s: malformed JSON message", s.remote)
			return
		}
		first := !s.activated
		if err := s.dispatch(env); err != nil {
			if !errors.Is(err, errGoodbye) {
				log.Printf("[sendspin] %s: %v", s.remote, err)
			}
			return
		}
		if first && s.activated {
			go s.timeSync(stopSync)
		}
	}
}

var errGoodbye = errors.New("sendspin: goodbye sent")

func (s *session) dispatch(env envelope) error {
	if !s.activated && env.Type != "server/activate" && env.Type != "noise/handshake" &&
		!(env.Type == "server/hello" && s.awaitHello) {

		return nil
	}
	switch env.Type {
	case "server/activate":
		var a serverActivate
		if err := json.Unmarshal(env.Payload, &a); err != nil {
			return fmt.Errorf("%w: server/activate: %v", errProtocol, err)
		}
		return s.onActivate(a)
	case "server/time":
		var t serverTime
		if json.Unmarshal(env.Payload, &t) == nil {
			s.onTime(t)
		}
	case "stream/start":
		var st streamStart
		if json.Unmarshal(env.Payload, &st) == nil && st.Player != nil {
			s.onStreamStart(*st.Player)
		}
	case "stream/clear":
		var r streamRoles
		json.Unmarshal(env.Payload, &r)
		if r.covers("player") && s.isAdmitted() {
			s.c.place.flush()
			s.regate()
		}
	case "stream/end":
		var r streamRoles
		json.Unmarshal(env.Payload, &r)
		if r.covers("player") {
			s.endStream()
		}
	case "group/update":
		var g groupUpdate
		if json.Unmarshal(env.Payload, &g) == nil {
			s.mu.Lock()
			s.group = g
			s.mu.Unlock()
			s.onPlaybackState(g.PlaybackState)
			s.c.changed()
		}
	case "server/state":
		var st serverState
		if json.Unmarshal(env.Payload, &st) == nil {
			s.onServerState(st)
		}
	case "server/command":
		var cmd serverCommand
		if json.Unmarshal(env.Payload, &cmd) == nil && cmd.Player != nil {
			s.onCommand(*cmd.Player)
		}
	case "server/unpair":
		if s.psk.cat != catLongTerm {
			return nil
		}
		s.c.store.removeRecord(s.serverID)
		log.Printf("[sendspin] %q unpaired this device", s.name())
		return s.goodbye("unpaired")
	case "server/pair-finalize":
		if s.stagedPSK != nil {
			err := s.c.store.addRecord(s.serverID, s.stagedPSK, s.c.inUse)
			s.stagedPSK = nil
			if err != nil {
				log.Printf("[sendspin] pairing record not saved: %v", err)
				return err
			}
			log.Printf("[sendspin] paired with %q", s.name())
			s.c.changed()
		}
	case "pair/abort":
		s.stagedPSK = nil
	case "noise/handshake":
		return s.rehandshake(env.Payload)
	case "server/hello":

		if s.awaitHello {
			s.awaitHello = false
			return s.sendJSON("client/hello", s.c.hello(), true)
		}
	}
	return nil
}

func allowed(cat pskCategory, acts map[string]bool, unpaired bool) bool {
	switch key := actKey(acts); cat {
	case catLongTerm:
		return key == "" || key == actPlayback
	default:
		switch key {
		case "", actPairing:
			return true
		case actPlayback, actPairing + "," + actPlayback:
			return unpaired
		}
	}
	return false
}

func actKey(acts map[string]bool) string {
	var ks []string
	for k := range acts {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

func withPlayback(acts map[string]bool) map[string]bool {
	m := map[string]bool{actPlayback: true}
	for k := range acts {
		m[k] = true
	}
	return m
}

func rank(acts map[string]bool) int {
	switch {
	case acts[actPlayback]:
		return 2
	case acts[actPairing]:
		return 1
	}
	return 0
}

func (s *session) onActivate(a serverActivate) error {
	acts := map[string]bool{}
	for _, x := range a.Activities {

		if x == actPlayback || x == actPairing {
			acts[x] = true
		}
	}
	s.mu.Lock()
	roles := s.roles
	s.mu.Unlock()
	if a.ActiveRoles != nil {
		roles = *a.ActiveRoles
	} else if !s.activated {
		roles = nil
	}

	unpaired := s.c.unpairedAccess()
	capable := allowed(s.psk.cat, withPlayback(acts), unpaired)
	if !capable && a.ActiveRoles == nil {
		roles = nil
	}
	if !allowed(s.psk.cat, acts, unpaired) || (len(roles) > 0 && !capable) {
		if s.psk.cat != catLongTerm && !unpaired &&
			allowed(s.psk.cat, acts, true) && (len(roles) == 0 || allowed(s.psk.cat, withPlayback(acts), true)) {
			log.Printf("[sendspin] %q wants playback without pairing; unpaired access is off", s.name())
			return s.goodbye("pairing_required")
		}
		return s.goodbye("unauthorized")
	}

	if !s.activated {
		if !s.c.admit(s, rank(acts)) {
			return s.goodbye("concurrent_attempt")
		}
		s.activated = true
		s.ws.SetReadDeadline(time.Now().Add(idleTimeout))
	} else {
		s.c.rerank(s, rank(acts))
	}
	s.activities = acts
	if acts[actPlayback] {
		s.c.store.touch(s.serverID, true)
	}

	wasPlayer := s.playerRole()
	s.mu.Lock()
	s.roles = roles
	reported := s.stateSent
	s.mu.Unlock()
	if wasPlayer && !s.playerRole() {
		s.endStream()
	}
	if s.playerRole() && (!wasPlayer || !reported) {
		s.sendState()
	}

	if acts[actPairing] {
		s.pairingIndex++
		return s.onPairing(a.Pairing)
	}
	if s.setQuiet(false) && s.playerRole() {
		s.sendState()
	}
	s.c.changed()
	return nil
}

func (s *session) setQuiet(q bool) bool {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	was := s.quiet
	s.quiet = q
	return was != q
}

func (s *session) onPairing(p *activatePairing) error {
	if p == nil || p.Method != methodPSK || s.psk.cat != catPairing {
		return s.sendJSON("pair/abort", pairAbort{Reason: "method_not_supported"}, true)
	}
	psk, err := randomPSK()
	if err != nil {
		return err
	}
	s.stagedPSK = psk
	s.setQuiet(true)

	return s.sendJSON("client/pair-finalize", pairFinalize{LongTermPSK: b64u(psk)}, true)
}

func (s *session) playerRole() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.roles {
		if r == rolePlayer {
			return true
		}
	}
	return false
}

func (s *session) name() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serverName
}

func (s *session) isAdmitted() bool { return s.c.isAdmitted(s) }

func (s *session) available() bool {
	if s.c.external() {
		return false
	}
	return s.filter.synchronized()
}

func (s *session) synced() bool { return s.filter.synchronized() }

func (s *session) sendState() {
	if !s.playerRole() {
		return
	}
	set := s.c.store.settings()
	s.mu.Lock()
	s.stateSent = true
	s.mu.Unlock()

	state := "synchronized"
	if !s.synced() {
		state = "error"
	}
	s.send("client/state", clientState{
		Available: s.available(),
		Player: &playerState{
			State:          state,
			Volume:         set.Volume,
			Muted:          set.Muted,
			StaticDelayMs:  0,
			OutputDelayMs:  0,
			RequiredLeadMs: s.c.requiredLeadMs,
			MinBufferMs:    s.c.minBufferMs(),

			SupportedCommands: []string{"volume", "mute", "set_output_delay"},
		},
	})
}

func (s *session) onTime(t serverTime) {
	now := nowUs()
	offset := float64((t.ServerReceived-t.ClientTransmitted)+(t.ServerTransmitted-now)) / 2
	delay := float64((now-t.ClientTransmitted)-(t.ServerTransmitted-t.ServerReceived)) / 2
	s.filter.update(pyRound(offset), pyRound(delay), now)
}

func (s *session) onServerState(st serverState) {
	s.mu.Lock()
	switch {
	case st.MetadataNull:
		s.metadata = streamMetadata{}
	case st.Metadata != nil:
		s.metadata = *st.Metadata
	}
	if st.Controller != nil {
		s.controller = st.Controller
	}
	s.mu.Unlock()
	s.c.changed()
}

func (s *session) onPlaybackState(state string) {
	s.mu.Lock()
	s.serverSaid = true
	switch state {
	case "playing":

		s.serverPlaying = true
		s.asked.Store("")
	case "paused":
		s.serverPlaying = false
		s.asked.Store("")

		s.settleStop()
	case "stopped":
		s.serverPlaying = false

		if s.askedFor() != "pause" {
			s.serverPlaying = true
		}
		s.asked.Store("")
	}
	s.mu.Unlock()
	s.regate()
}

const (
	stoppedFor = 30 * time.Minute
)

func (s *session) settleStop() {
	s.mu.Lock()
	generation := s.settleGen.Add(1)
	s.mu.Unlock()

	time.AfterFunc(stoppedFor, func() {
		s.mu.Lock()
		same := s.settleGen.Load() == generation
		s.mu.Unlock()
		if !same {
			return
		}
		s.onPlaybackState("stopped")
	})
}

func (s *session) askedFor() string {
	v, _ := s.asked.Load().(string)
	return v
}

func (s *session) regate() {
	s.mu.Lock()
	playing := s.serverPlaying
	said := s.serverSaid
	s.mu.Unlock()

	ready := s.c.place.Ready()

	s.c.place.hold(!said || !playing || !ready)
}

func (s *session) Controller() *controllerState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.controller == nil {
		return nil
	}
	out := *s.controller
	out.SupportedCommands = append([]string(nil), s.controller.SupportedCommands...)
	return &out
}

func (s *session) timeSync(stop <-chan struct{}) {
	for {
		if err := s.send("client/time", clientTime{ClientTransmitted: nowUs()}); err != nil {
			return
		}
		s.noteClock()
		select {
		case <-stop:
			return
		case <-time.After(time.Duration(s.filter.syncIntervalMs()) * time.Millisecond):
		}
	}
}

func (s *session) noteClock() {
	if !s.filter.synchronized() {
		return
	}
	s.mu.Lock()
	already := s.clockNoted
	s.clockNoted = true
	s.mu.Unlock()
	if already {
		return
	}
	s.sendState()
}

func (s *session) onStreamStart(f streamFormat) {
	d, err := newDecoder(f)
	if err != nil {
		log.Printf("[sendspin] %v", err)
		s.dec = nil
		return
	}
	if !s.streaming && s.isAdmitted() {
		s.c.place.flush()
		s.c.place.resetStats()
	}

	if s.isAdmitted() {
		s.c.place.openAgain()
		s.regate()
	}
	s.dec, s.streaming = d, true
	s.c.changed()
}

func (s *session) playing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.dec != nil
}

func (s *session) playingMusic() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serverPlaying && s.serverSaid && !s.heldForPause()
}

func (s *session) heldForPause() bool {
	asked, _ := s.asked.Load().(string)
	return asked == "pause"
}

func (s *session) endStream() {
	if s.streaming && s.isAdmitted() {
		s.c.place.flush()
		s.regate()
	}

	s.streaming = false
	s.c.changed()
}

func (s *session) onAudio(msg []byte) {

	if s.dec == nil || !s.isAdmitted() || s.c.external() {
		return
	}

	ts, payload, err := chunkPayload(msg, s.dec.f.Codec)
	if err != nil {
		return
	}
	pcm, err := s.dec.decode(make([]int16, 0, 4096), payload)
	if err != nil {
		log.Printf("[sendspin] decode: %v", err)
		return
	}

	s.c.place.write(ts, pcm)
	s.mu.Lock()
	s.lastAudio = time.Now()
	s.mu.Unlock()
}

func (s *session) onCommand(c playerCommand) {
	set := s.c.store.settings()
	switch c.Command {
	case "volume":
		if c.Volume != nil {
			set.Volume = clamp(*c.Volume, 0, 100)
		}
	case "mute":
		if c.Mute != nil {
			set.Muted = *c.Mute
		}
	case "set_static_delay", "set_output_delay":
		v := c.StaticDelayMs
		if v == nil {
			v = c.OutputDelayMs
		}
		if v != nil {

			set.StaticDelayMs = clamp(*v, 0, maxDelayMs)
		}
	default:
		return
	}
	s.c.applySettings(set)
	s.sendState()
}

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }

func (s *session) send(typ string, payload any) error {
	return s.sendJSON(typ, payload, false)
}

func (s *session) sendJSON(typ string, payload any, force bool) error {
	b, err := marshalMsg(typ, payload)
	if err != nil {
		return err
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.closed {
		return errors.New("sendspin: session closed")
	}
	if s.quiet && !force {
		return nil
	}
	ct, err := s.tr.encrypt(append([]byte{msgJSON}, b...))
	if err != nil {
		return err
	}
	s.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.ws.WriteMessage(websocket.BinaryMessage, ct)
}

func (s *session) goodbye(reason string) error {

	s.sendJSON("client/goodbye", clientGoodbye{Reason: reason}, true)
	log.Printf("[sendspin] goodbye to %q: %s", s.name(), reason)
	return errGoodbye
}

func (s *session) readMsg() ([]byte, error) {
	mt, data, err := s.ws.ReadMessage()
	if err != nil {
		return nil, err
	}
	if mt != websocket.BinaryMessage {

		return nil, fmt.Errorf("%w: cleartext frame after handshake", errProtocol)
	}
	pt, err := s.tr.decrypt(data)
	if err != nil {
		return nil, fmt.Errorf("%w: transport message failed authentication", errProtocol)
	}
	return s.ra.push(pt)
}

func (s *session) readJSON() (string, json.RawMessage, error) {
	for {
		msg, err := s.readMsg()
		if err != nil {
			return "", nil, err
		}
		if msg == nil {
			continue
		}
		if msg[0] != msgJSON {
			return "", nil, fmt.Errorf("%w: binary message before activation", errProtocol)
		}
		var env envelope
		if err := json.Unmarshal(msg[1:], &env); err != nil {
			return "", nil, err
		}
		return env.Type, env.Payload, nil
	}
}

func (s *session) close() {
	s.sendMu.Lock()
	s.closed = true
	s.sendMu.Unlock()
	s.ws.Close()
	s.c.release(s)
}

func (s *session) readText(what string) (envelope, []byte, error) {
	s.ws.SetReadDeadline(time.Now().Add(handshakeTimeout))
	mt, data, err := s.ws.ReadMessage()
	if err != nil {
		return envelope{}, nil, fmt.Errorf("awaiting %s: %w", what, err)
	}
	if mt != websocket.TextMessage {
		return envelope{}, nil, fmt.Errorf("awaiting %s: binary frame", what)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return envelope{}, nil, fmt.Errorf("awaiting %s: %v", what, err)
	}
	return env, data, nil
}

func (s *session) handshake() error {
	id := s.c.store.id
	initBytes, err := marshalMsg("client/init", clientInit{ClientID: id.clientID(), Version: 1, Suite: suiteName})
	if err != nil {
		return err
	}
	s.ws.SetWriteDeadline(time.Now().Add(handshakeTimeout))
	if err := s.ws.WriteMessage(websocket.TextMessage, initBytes); err != nil {
		return err
	}

	env, srvInitBytes, err := s.readText("server/init")
	if err != nil {
		return err
	}
	if env.Type == "server/error" {
		var e serverError
		json.Unmarshal(env.Payload, &e)
		return fmt.Errorf("server refused client/init: %s", e.Reason)
	}
	var si serverInit
	if env.Type != "server/init" || json.Unmarshal(env.Payload, &si) != nil || si.Version != 1 {
		return fmt.Errorf("%w: bad server/init", errProtocol)
	}
	serverPub, err := peerKey(si.ServerID)
	if err != nil {
		return err
	}
	s.serverID = si.ServerID
	s.mu.Lock()
	s.serverName = si.ServerID[:8]
	s.mu.Unlock()

	prologue := append(append([]byte(nil), initBytes...), srvInitBytes...)
	env, _, err = s.readText("noise message 1")
	if err != nil {
		return err
	}
	tr, h, psk, msg2, err := s.respond(env, serverPub, prologue)
	if err != nil {
		return err
	}
	out, _ := marshalMsg("noise/handshake", noiseHandshake{Data: b64u(msg2)})
	if err := s.ws.WriteMessage(websocket.TextMessage, out); err != nil {
		return err
	}
	s.mu.Lock()
	s.psk = psk
	s.mu.Unlock()
	s.tr, s.h = tr, h
	return nil
}

func (s *session) respond(env envelope, serverPub, prologue []byte) (*transport, []byte, resolvedPSK, []byte, error) {
	var hs noiseHandshake
	if env.Type != "noise/handshake" || json.Unmarshal(env.Payload, &hs) != nil {
		return nil, nil, resolvedPSK{}, nil, fmt.Errorf("%w: expected noise/handshake", errProtocol)
	}
	msg1, err := unb64u(hs.Data)
	if err != nil {
		return nil, nil, resolvedPSK{}, nil, err
	}
	r, err := newResponder(s.c.store.id, serverPub, prologue)
	if err != nil {
		return nil, nil, resolvedPSK{}, nil, err
	}
	p, err := r.readMsg1(msg1)
	if err != nil {
		return nil, nil, resolvedPSK{}, nil, err
	}
	psk := s.c.store.resolve(p.PskID)
	switch p.PskCategory {
	case "lt":
		if psk.cat != catLongTerm {
			psk = resolvedPSK{psk: sentinelPSK, cat: catSentinel}
		}
	case "pr":
		if psk.cat != catPairing {
			psk = resolvedPSK{psk: sentinelPSK, cat: catSentinel}
		}
	}

	if psk.cat == catLongTerm && psk.serverID != s.serverID {
		return nil, nil, resolvedPSK{}, nil, errors.New("PSK is bound to a different server")
	}
	msg2, tr, h, err := r.writeMsg2(psk.psk)
	if err != nil {
		return nil, nil, resolvedPSK{}, nil, err
	}
	return tr, h, psk, msg2, nil
}

func (s *session) rehandshake(payload json.RawMessage) error {
	serverPub, _ := peerKey(s.serverID)
	s.setQuiet(true)

	tr, h, psk, msg2, err := s.respond(envelope{Type: "noise/handshake", Payload: payload}, serverPub, s.h)
	if err != nil {
		return fmt.Errorf("re-handshake: %w", err)
	}
	if err := s.sendJSON("noise/handshake", noiseHandshake{Data: b64u(msg2)}, true); err != nil {
		return err
	}
	s.sendMu.Lock()
	s.tr = tr
	s.sendMu.Unlock()
	s.awaitHello = true
	s.mu.Lock()
	s.psk = psk
	s.mu.Unlock()
	s.h, s.pairingIndex = h, 0
	log.Printf("[sendspin] %q: session re-keyed (%s)", s.name(), psk.cat)
	s.c.changed()
	return nil
}

func (c pskCategory) String() string {
	switch c {
	case catLongTerm:
		return "paired"
	case catPairing:
		return "pairing key"
	}
	return "unpaired"
}
