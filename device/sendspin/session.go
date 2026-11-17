package sendspin

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	handshakeTimeout = 30 * time.Second
	// A connection that has not declared its purpose in 30s is dropped.
	activateTimeout = 30 * time.Second
	// Past activation the device asks for the time every 3s at most, so a
	// silent link for this long is a dead one.
	idleTimeout = 30 * time.Second

	rolePlayer = "player@v1"
	// roleMetadata is the role that carries what is playing.
	//
	// A client that declares it is sent the metadata: the title, the
	// artist, the album, the artwork and where the track has got to.
	// Without it the server has no way of telling the device what it is
	// playing, because the audio itself says nothing about that.
	roleMetadata = "metadata@v1"
	// roleController is the role that drives the transport.
	//
	// A client that declares it is sent the list of commands the server
	// will accept, and may send commands of its own: play, pause, the
	// next track and the one after it, and seeking.
	roleController = "controller@v1"
	actPlayback    = "playback"
	actPairing     = "pairing"
	methodPSK      = "pairing_psk"
	maxReadBytes   = 4 << 20
)

// session is one server's WebSocket. The read loop owns almost everything;
// sends come from several goroutines and are serialised by sendMu, which
// also covers the transport, since Noise nonces must go out in order.
type session struct {
	c      *Client
	ws     *websocket.Conn
	remote string

	sendMu sync.Mutex
	tr     *transport
	// quiet holds back everything but the exchange itself: from the start
	// of a pairing or re-handshake until the server/activate that ends it.
	// The server reads that stretch as a strict sequence, and a client/time
	// arriving inside it fails the pairing (9.1.1, measured by the interop
	// test).
	quiet  bool
	closed bool

	filter *timeFilter

	// Read-loop state.
	h            []byte
	serverID     string
	ra           reassembler
	activated    bool
	activities   map[string]bool
	pairingIndex int
	stagedPSK    []byte
	awaitHello   bool // re-keyed; 9.1.1 re-sends server/hello next
	dec          *decoder
	streaming    bool

	// Written by the read loop, read from other goroutines too.
	mu         sync.Mutex
	serverName string
	psk        resolvedPSK
	roles      []string
	stateSent  bool // a client/state has gone out on this connection
	clockNoted bool // the clock has been reported good, once
	group      groupUpdate
	lastAudio  time.Time
	// metadata is what the server says is playing.
	//
	// It is separate from the group and the player: this is the music
	// itself — the title, the artist, the album, the artwork and where
	// the track has got to.
	metadata streamMetadata
	// controller is what the server says the transport can do.
	//
	// It is nil until the server says, and the panel only offers the
	// controls it has been told are there.
	controller *controllerState
}

func newSession(c *Client, ws *websocket.Conn) *session {
	return &session{c: c, ws: ws, remote: ws.RemoteAddr().String(), filter: newTimeFilter()}
}

var errProtocol = errors.New("sendspin: protocol error")

// run drives the connection to its end. Every exit path closes the socket.
func (s *session) run() {
	defer s.close()
	s.ws.SetReadLimit(maxReadBytes)
	if err := s.handshake(); err != nil {
		log.Printf("[sendspin] %s: handshake failed: %v", s.remote, err)
		return
	}
	log.Printf("[sendspin] %s: encrypted session with %q (%s)", s.remote, s.name(), s.psk.cat)

	// server/hello, then ours.
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
			continue // a role we do not implement
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

// errGoodbye ends the connection after a client/goodbye already went out.
var errGoodbye = errors.New("sendspin: goodbye sent")

func (s *session) dispatch(env envelope) error {
	if !s.activated && env.Type != "server/activate" && env.Type != "noise/handshake" &&
		!(env.Type == "server/hello" && s.awaitHello) {
		// A server may not send this yet. Ignored rather than fatal: the
		// cost of tolerating it is nothing, of closing a working link a lot.
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
			return nil // unpaired session: nothing to drop
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
		// 9.1.1 redoes the hello exchange after every re-handshake; the
		// spec says neither hello is re-sent. Answer it when it comes.
		if s.awaitHello {
			s.awaitHello = false
			return s.sendJSON("client/hello", s.c.hello(), true)
		}
	}
	return nil
}

// ── activation and admission ───────────────────────────────────────────────

// allowedSets is which activity sets a server may declare under each PSK.
// Playback on an unpaired session needs unpaired access enabled.
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

// rank orders connections for admission: playback over pairing over nothing.
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
		// 9.1.1 defines a "management" activity the device does not offer;
		// an activity it does not know is ignored like any unknown value.
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
		roles = nil // persisted roles lapse when playback no longer could happen
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
		s.sendState() // the exchange suppressed it; bring the server up to date
	}
	s.c.changed()
	return nil
}

// setQuiet sets the exchange state, reporting whether it changed.
func (s *session) setQuiet(q bool) bool {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	was := s.quiet
	s.quiet = q
	return was != q
}

// onPairing runs the Pairing PSK flow, the one method the device offers: the
// operator pasted the device's token into the server, so the handshake itself
// authenticated both sides, and the device mints the long-term PSK.
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
	// 9.1.1: the Pairing PSK flow is client/pair-finalize alone. The current
	// spec puts a client/pair-init before it, which 9.1.1 rejects as out of
	// sequence and fails the pairing.
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

// ── player ──────────────────────────────────────────────────────────────────

// available is what client/state reports: the panel can play, and Home
// Assistant has not taken the music plane.
//
// It is false until the clock filter has converged. A player that says it
// can play before its clock is good would be scheduled against a wrong
// offset, and the server would send the audio to the wrong moment.
func (s *session) available() bool {
	if s.c.external() {
		return false
	}
	return s.filter.synchronized()
}

// synced says the clock is good, for the state field.
func (s *session) synced() bool { return s.filter.synchronized() }

// sendState reports availability and the player's state. It is sent as soon
// as the role activates: Music Assistant waits for this state before it
// starts the stream. Audio stays queued until the clock filter is ready.
func (s *session) sendState() {
	if !s.playerRole() {
		return
	}
	set := s.c.store.settings()
	s.mu.Lock()
	s.stateSent = true
	s.mu.Unlock()
	// The state says whether the panel is in step with the server's clock.
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
			// The handlers for all three commands are implemented below.
			// Omitting volume and mute here made Music Assistant treat the
			// speaker as having no controllable volume and display 0%.
			SupportedCommands: []string{"volume", "mute", "set_output_delay"},
		},
	})
}

// onTime feeds the clock filter a time exchange.
//
// The four timestamps are all Unix microseconds: ours when we sent the
// request, the server's when it received and when it replied, and ours when
// the reply arrived. The offset is the middle of the two ways of measuring
// how far apart the clocks are, and the uncertainty is half the round trip.
//
// Mixing the two domains here would put the offset out by decades, and the
// filter would then place the audio that far from where it belongs.
func (s *session) onTime(t serverTime) {
	now := nowUs()
	offset := float64((t.ServerReceived-t.ClientTransmitted)+(t.ServerTransmitted-now)) / 2
	delay := float64((now-t.ClientTransmitted)-(t.ServerTransmitted-t.ServerReceived)) / 2
	s.filter.update(pyRound(offset), pyRound(delay), now)
}

// onServerState records what the server says is playing.
//
// The metadata is what the panel shows in its drawer: the title, the
// artist, the album, the artwork and where the track has got to.
//
// It is separate from the player's own state, because the player is
// about the sound and this is about the music.
//
// A null metadata means the music has stopped, and the panel is told
// so by being told there is nothing.
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

// onPlaybackState is what the server says it is doing.
//
// Music Assistant does not pause a Sendspin stream: it ends it, clears the
// track and lets the output go. It resumes only a player it sees as
// stopped — one it sees as paused is sent a plain "unpause" that nothing
// here can act on, and the room says playing in silence.
//
// So the panel says what the server said. A pause stops the audio and
// keeps the track, so that a play starts it again. A stop stops it and
// lets go, so that a play starts the queue again where it was.
//
// A stop from the server while it was playing, that nobody here asked for,
// is Music Assistant's own pause as often as it is a stop, and it is said
// as a pause first: Music Assistant keeps the place in the track when a
// player goes from playing to paused to stopped, as it does after a pause
// from the screen, and starts the track over when it goes straight from
// playing to stopped.
func (s *session) onPlaybackState(state string) {
	switch state {
	case "paused", "stopped":
		s.hold(true)
	case "playing":
		s.hold(false)
	}
}

// hold stops or starts the audio without letting go of the track.
//
// The placement is flushed, so the audio that was queued is dropped and
// the next chunk is placed afresh: a pause means the timeline is about to
// change, and what was queued belongs to the timeline before it.
//
// The hold is released when the server says it is playing again, and not
// only when a new stream starts: the server ends the stream on a pause and
// resumes the same one, and it says it is playing before it says it has
// started.
func (s *session) hold(paused bool) {
	s.c.place.hold(paused)
}

// Controller says what the transport can do.
//
// It is nil until the server says, and the panel only offers the
// controls that have been told to it.
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

// timeSync keeps the filter fed for as long as the session lives.
//
// It also tells the server when the clock has become good, because the
// player says it is unavailable until then. The state is sent once, when
// the clock first converges, and not again: the filter's quality is read
// as lost whenever five seconds pass without a round, and following that
// would take the player away between every two rounds — which stops the
// server streaming to it.
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

// noteClock tells the server the player is available, the first time the
// clock filter is good.
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
	// The placement is opened for the stream: it is what the speaker pulls
	// from, and what the server's audio is placed into. A stream that
	// starts releases the hold a pause put on it.
	if s.isAdmitted() {
		s.c.place.openAgain()
	}
	s.dec, s.streaming = d, true
	s.c.changed()
}

// playing says whether the server is playing and the audio is coming.
//
// It is not whether the queue happens to have audio in it: the queue is
// empty between two chunks and after a clear, and the speaker's source is
// attached on this answer.
func (s *session) playing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The decoder is what says the server is sending audio, and it is
	// kept across the end of a stream: the server ends a stream on a
	// pause and resumes the same one.
	return s.dec != nil
}

// endStream is the server saying the stream is over.
//
// The spec has stream/end stop the output and clear the buffers, and the
// server sends it on a stop, a skip and a seek. A track running into the
// next one keeps the stream and says nothing.
//
// So the placement is not closed here: it is held, and the audio that
// arrives while it is held is dropped. Closing it would leave the
// placement closed until a new stream started, and the server does not
// necessarily start a new one — it may resume the stream it just ended,
// which is what a pause and a play is. The audio would then never be
// placed again, and the room would say playing in silence.
func (s *session) endStream() {
	if s.streaming && s.isAdmitted() {
		s.c.place.hold(true)
	}
	// The decoder is KEPT.
	//
	// The server ends the stream on a stop, a skip and a seek, and then
	// resumes the same one: a pause and a play. A new stream/start comes
	// with a new track, not with a resumed one. So the decoder is kept, or
	// the audio that arrives after the resume would be discarded for
	// want of one, and the room would say playing in silence.
	s.streaming = false
	s.c.changed()
}

func (s *session) onAudio(msg []byte) {
	// Audio for a connection that is not the admitted one, or while HA has
	// the music plane, is discarded — valid, and never a reason to close.
	//
	// A stream that was ended and is being resumed still has its decoder:
	// the server ends a stream on a pause and resumes the same one.
	if s.dec == nil || !s.isAdmitted() || s.c.external() {
		return
	}
	// And it is not placed while the audio is held.
	if s.c.place.Held() {
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
	// The audio is placed, not queued: the frame its timestamp asks for is
	// where it goes, and the speaker takes it from there. A queue would
	// have to be emptied by asking what is due, and that asks the clock
	// rather than the frame counter, and the clock is what jitters.
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
			set.StaticDelayMs = clamp(*v, 0, 5000)
		}
	default:
		return
	}
	s.c.applySettings(set)
	s.sendState()
}

func clamp(v, lo, hi int) int { return max(lo, min(hi, v)) }

// ── transport ───────────────────────────────────────────────────────────────

// send encrypts and writes one JSON message. Suppressed during a
// re-handshake, when the spec allows nothing but the handshake itself.
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
	// A goodbye goes out even mid-exchange: it precedes the close.
	s.sendJSON("client/goodbye", clientGoodbye{Reason: reason}, true)
	log.Printf("[sendspin] goodbye to %q: %s", s.name(), reason)
	return errGoodbye
}

// readMsg returns the next complete decrypted message, or nil while a
// fragmented one is being collected.
func (s *session) readMsg() ([]byte, error) {
	mt, data, err := s.ws.ReadMessage()
	if err != nil {
		return nil, err
	}
	if mt != websocket.BinaryMessage {
		// Cleartext after transport mode is a silent failure.
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

// ── handshake ───────────────────────────────────────────────────────────────

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

	// The prologue binds the cleartext exchange: the exact bytes as they
	// crossed the wire, never a re-encoding.
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

// respond runs the responder side of one handshake given message 1.
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
	// A long-term PSK is bound to the server it was made with.
	if psk.cat == catLongTerm && psk.serverID != s.serverID {
		return nil, nil, resolvedPSK{}, nil, errors.New("PSK is bound to a different server")
	}
	msg2, tr, h, err := r.writeMsg2(psk.psk)
	if err != nil {
		return nil, nil, resolvedPSK{}, nil, err
	}
	return tr, h, psk, msg2, nil
}

// rehandshake answers an in-band handshake: the server moving the session to
// a new PSK, as it does after pairing. Message 2 goes out under the OLD keys;
// everything after it under the new ones.
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
	s.tr = tr // stays quiet until the server/activate that follows
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
