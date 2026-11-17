package sendspin

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/grandcat/zeroconf"
)

// Port and path are the spec's recommendations, which is also where Music
// Assistant looks if a user types the address by hand.
const (
	DefaultPort = 8928
	Path        = "/sendspin"
	serviceType = "_sendspin._tcp"
	maxSessions = 4
)

// Config is what the device supplies. Everything else is Sendspin's own.
type Config struct {
	StorePath string // persisted identity, pairing records and player settings
	Name      string // shown in Music Assistant
	Instance  string // mDNS instance name; unique on the LAN
	Port      int
	// Addresses are the addresses Music Assistant can reach. The mDNS
	// announcement is limited to interfaces that own these addresses so
	// Docker bridges cannot publish an unreachable address for the host.
	Addresses []net.IP
	Product   string
	Version   string
	// Unpaired lets a server play without pairing, once its operator has
	// approved the device. Off by default: anyone on the LAN can claim to
	// be a server, and pairing is one paste of the device's token.
	Unpaired bool
	// RequiredLeadTime and MinBuffer describe the output backend's startup
	// and jitter-buffer needs. Zero selects conservative 2s defaults.
	RequiredLeadTime time.Duration
	MinBuffer        time.Duration
}

// Client is the device's Sendspin player. Servers connect to it (the spec's
// recommended direction, and the one Music Assistant uses): it advertises
// itself over mDNS and listens.
type Client struct {
	cfg   Config
	store *store
	// place is where the server's audio goes: the frames it asks for, held
	// until the speaker pulls them.
	place *placement

	mu               sync.Mutex
	admitted         *session
	rankOf           map[*session]int
	sessions         map[*session]bool
	extBusy          bool
	bufferMs         int // declared min_buffer_ms, above minBufferFloorMs when set
	minBufferFloorMs int
	requiredLeadMs   int
	unpaired         bool
	onChange         func()
	onSettings       func(playerSettings)
	// onVolume, when set, makes the server's volume the DEVICE's volume:
	// commands go to it rather than to a gain on synced music alone, and the
	// device reports its level back through SetVolume. One volume, whoever
	// moves it (Wil, 2026-09-30).
	onVolume func(v int)

	srv  *http.Server
	mdns *zeroconf.Server
}

// New loads (or creates) the device's Sendspin identity. It does not listen
// until Start.
func New(cfg Config) (*Client, error) {
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	// The lead and the buffer are small, and that is deliberate.
	//
	// The server holds the audio back by the buffer before it sends it,
	// so a big buffer is a big delay before anything is heard. And the
	// player compares the audio's own time with the clock and plays it
	// when it is due, so an audio that is a whole buffer in the future
	// is an audio that is not played until the buffer has passed.
	//
	// A quarter of a second is enough for the network not to outrun
	// the speaker.
	if cfg.RequiredLeadTime <= 0 {
		cfg.RequiredLeadTime = 250 * time.Millisecond
	}
	if cfg.MinBuffer <= 0 {
		cfg.MinBuffer = 250 * time.Millisecond
	}
	st, err := openStore(cfg.StorePath)
	if err != nil {
		return nil, fmt.Errorf("sendspin state %s: %w", cfg.StorePath, err)
	}
	c := &Client{
		cfg:              cfg,
		store:            st,
		rankOf:           map[*session]int{},
		sessions:         map[*session]bool{},
		unpaired:         cfg.Unpaired,
		minBufferFloorMs: int(cfg.MinBuffer.Milliseconds()),
		requiredLeadMs:   int(cfg.RequiredLeadTime.Milliseconds()),
	}
	c.place = newPlacement(newTimeFilter())

	set := st.settings()
	c.place.setGain(c.gainVolume(set.Volume), set.Muted)
	c.place.setDelay(set.StaticDelayMs)
	return c, nil
}

// ClientID is the device's Sendspin identity (its public key). Not secret.
func (c *Client) ClientID() string { return c.store.id.clientID() }

// PairingToken is what the user pastes into Music Assistant to pair. It
// carries the pairing PSK, so it is a secret: never log it.
func (c *Client) PairingToken() string { return c.store.token() }

// OnChange is called (on its own goroutine) whenever the status moves.
func (c *Client) OnChange(f func()) {
	c.mu.Lock()
	c.onChange = f
	c.mu.Unlock()
}

func (c *Client) changed() {
	c.mu.Lock()
	f := c.onChange
	c.mu.Unlock()
	if f != nil {
		go f()
	}
}

// Start listens and advertises. It returns once both are up.
func (c *Client) Start() error {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(c.cfg.Port))
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	up := websocket.Upgrader{
		// Not a browser endpoint: the Noise handshake authenticates, and a
		// server has no Origin worth checking.
		CheckOrigin: func(*http.Request) bool { return true },
	}
	mux.HandleFunc(Path, func(w http.ResponseWriter, r *http.Request) {
		// A cap on open connections: anything on the LAN can open one and
		// hold it for the 30s handshake window, and each costs a goroutine
		// and buffers. The spec lets a client refuse past a cap. Four covers
		// a playback server, a pairing one and a displacement in flight.
		c.mu.Lock()
		full := len(c.sessions) >= maxSessions
		c.mu.Unlock()
		if full {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		ws, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s := newSession(c, ws)
		c.mu.Lock()
		c.sessions[s] = true
		c.mu.Unlock()
		go s.run()
	})
	c.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go c.srv.Serve(ln)

	interfaces, err := announceInterfaces(c.cfg.Addresses)
	if err != nil {
		c.srv.Close()
		return fmt.Errorf("mDNS interfaces: %w", err)
	}
	c.mdns, err = zeroconf.Register(c.cfg.Instance, serviceType, "local.", c.cfg.Port,
		[]string{"path=" + Path, "name=" + c.cfg.Name}, interfaces)
	if err != nil {
		c.srv.Close()
		return fmt.Errorf("mDNS: %w", err)
	}
	log.Printf("[sendspin] player %q listening on :%d%s", c.cfg.Name, c.cfg.Port, Path)
	return nil
}

// announceInterfaces limits mDNS to the interfaces carrying reachable
// addresses. Advertising on every interface lets Docker bridges win the
// hostname's address record, making LAN clients connect to an unreachable IP.
func announceInterfaces(addresses []net.IP) ([]net.Interface, error) {
	if len(addresses) == 0 {
		return nil, nil
	}
	all, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var chosen []net.Interface
	seen := make(map[int]bool, len(addresses))
	for _, iface := range all {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}
			for _, wanted := range addresses {
				if ip.Equal(wanted) {
					if !seen[iface.Index] {
						chosen = append(chosen, iface)
						seen[iface.Index] = true
					}
					break
				}
			}
		}
	}
	if len(chosen) == 0 {
		return nil, fmt.Errorf("no network interface owns a published address")
	}
	return chosen, nil
}

// Stop says goodbye to every server and stops listening.
func (c *Client) Stop(reason string) {
	c.mu.Lock()
	var all []*session
	for s := range c.sessions {
		all = append(all, s)
	}
	c.mu.Unlock()
	for _, s := range all {
		s.sendJSON("client/goodbye", clientGoodbye{Reason: reason}, true)
		s.ws.Close()
	}
	if c.mdns != nil {
		c.mdns.Shutdown()
	}
	if c.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		c.srv.Shutdown(ctx)
		cancel()
	}
	c.place.flush()
}

// SetUnpaired changes whether servers may play without pairing. A change
// restarts every connection (goodbye "restart"), since client/hello
// advertised the old value and is sent once per connection.
func (c *Client) SetUnpaired(on bool) {
	c.mu.Lock()
	if c.unpaired == on {
		c.mu.Unlock()
		return
	}
	c.unpaired = on
	var all []*session
	for s := range c.sessions {
		all = append(all, s)
	}
	c.mu.Unlock()
	for _, s := range all {
		s.sendJSON("client/goodbye", clientGoodbye{Reason: "restart"}, true)
		s.ws.Close()
	}
}

func (c *Client) unpairedAccess() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.unpaired
}

// SetExternal is set while something other than Sendspin owns the music
// plane — Home Assistant's own media player. Decided 2026-08-22 (Wil): HA
// wins, since it is the user's direct request. The device reports itself
// unavailable, which moves it out of its group and stops the stream, and
// the server does not rejoin it afterwards: someone restarts the group.
func (c *Client) SetExternal(busy bool) {
	c.mu.Lock()
	if c.extBusy == busy {
		c.mu.Unlock()
		return
	}
	c.extBusy = busy
	s := c.admitted
	c.mu.Unlock()
	if busy {
		c.place.flush()
	}
	if s != nil {
		s.sendState()
	}
	c.changed()
}

// SetMinBuffer sets the minimum jitter buffer the player declares, never
// below the configured floor. A change reaches the server at once; on a live
// stream it costs one gap of the difference while the lead grows.
func (c *Client) SetMinBuffer(d time.Duration) {
	ms := max(int(d.Milliseconds()), c.minBufferFloorMs)
	c.mu.Lock()
	if c.bufferMs == ms {
		c.mu.Unlock()
		return
	}
	c.bufferMs = ms
	s := c.admitted
	c.mu.Unlock()
	if s != nil {
		s.sendState()
	}
}

func (c *Client) minBufferMs() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return max(c.bufferMs, c.minBufferFloorMs)
}

func (c *Client) external() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.extBusy
}

// OnSettings is told when a server changes volume, mute or delay, so the
// device can reflect it (they are also persisted here).
func (c *Client) OnSettings(f func(volume int, muted bool, delayMs int)) {
	c.mu.Lock()
	c.onSettings = func(p playerSettings) { f(p.Volume, p.Muted, p.StaticDelayMs) }
	c.mu.Unlock()
}

// OnVolume hands volume commands to the device (see onVolume). Set before
// Start.
func (c *Client) OnVolume(f func(v int)) {
	c.mu.Lock()
	c.onVolume = f
	c.mu.Unlock()
	set := c.store.settings()
	c.place.setGain(c.gainVolume(set.Volume), set.Muted)
}

// OnServerVolume is told the volume the server asked for.
//
// It is the same thing OnVolume is for — the server's volume becomes the
// device's volume — but it is a separate hook, because the two answer
// different questions. OnVolume is the command path: the server says
// "set the volume to this" and the device does it. OnServerVolume is
// the report path: the server says what it has settled on and the
// device shows it.
//
// They are separate because a device that owns the volume has to report
// its level back to the server, and a server that is told its own
// volume back would answer with a command, and the two would talk for
// ever.
//
// The player's own gain is dropped when this is set: the device applies
// the level to everything it plays, in one place, and a second gain on
// the music alone is a level applied in two places.
func (c *Client) OnServerVolume(f func(v int)) {
	c.mu.Lock()
	c.onVolume = f
	c.mu.Unlock()
	set := c.store.settings()
	// The player applies no gain of its own: the device's level is the
	// level, and it is applied to the music with everything else.
	c.place.setGain(100, set.Muted)
}

// SetVolume reports the device's volume (0-100) when it changes by any
// route: its buttons, Home Assistant, or a command this client passed on.
// Sent to the server only when it differs from what it last heard.
func (c *Client) SetVolume(v int) {
	set := c.store.settings()
	if set.Volume == v {
		return
	}
	set.Volume = v
	c.store.setSettings(set)
	c.mu.Lock()
	s := c.admitted
	c.mu.Unlock()
	if s != nil {
		s.sendState()
	}
	c.changed()
}

// gainVolume is the volume the player itself applies: none when the device
// owns the volume, since the device applies it to everything it plays.
func (c *Client) gainVolume(v int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onVolume != nil {
		return 100
	}
	return v
}

// ── transport ──────────────────────────────────────────────────────────────
//
// The commands are the controller role's. The panel's transport buttons
// come through here, and so does anything else that wants to drive the
// music.

// TransportCommands says what the transport can do.
//
// It is empty when there is no server, or when the server has not said,
// and the panel offers no buttons then. A control that does nothing is
// worse than no control.
func (c *Client) TransportCommands() []string {
	c.mu.Lock()
	s := c.admitted
	c.mu.Unlock()
	if s == nil {
		return nil
	}
	controller := s.Controller()
	if controller == nil {
		return nil
	}
	// The list is copied, because the session owns it and the caller
	// may keep it.
	return append([]string(nil), controller.SupportedCommands...)
}

// CanTransport says whether the transport can do a command.
//
// The server decides which commands it will accept, so the panel is
// told rather than guessing.
func (c *Client) CanTransport(command string) bool {
	for _, name := range c.TransportCommands() {
		if name == command {
			return true
		}
	}
	return false
}

// Transport sends a command to the transport.
//
// The commands are the ones the server says it supports, and it checks
// them again, so a command it does not want is dropped there.
func (c *Client) Transport(command string) bool {
	return c.transport(&controllerCommand{Command: command})
}

// SeekTo moves the music to a position, in milliseconds.
func (c *Client) SeekTo(positionMs int64) bool {
	return c.transport(&controllerCommand{Command: "seek", PositionMs: &positionMs})
}

// SeekRelative moves the music by an offset, in milliseconds. The offset
// is signed: negative goes back.
func (c *Client) SeekRelative(offsetMs int64) bool {
	return c.transport(&controllerCommand{Command: "seek_relative", OffsetMs: &offsetMs})
}

// transport sends one controller command to the admitted server.
func (c *Client) transport(cmd *controllerCommand) bool {
	c.mu.Lock()
	s := c.admitted
	c.mu.Unlock()
	if s == nil {
		return false
	}
	return s.send("client/command", clientCommand{Controller: cmd}) == nil
}

func (c *Client) applySettings(p playerSettings) {
	prev := c.store.settings()
	c.store.setSettings(p)
	c.mu.Lock()
	vf := c.onVolume
	c.mu.Unlock()
	if vf != nil && p.Volume != prev.Volume {
		vf(p.Volume)
	}
	c.place.setGain(c.gainVolume(p.Volume), p.Muted)
	c.place.setDelay(p.StaticDelayMs)
	c.mu.Lock()
	f := c.onSettings
	c.mu.Unlock()
	if f != nil {
		f(p)
	}
	c.changed()
}

// Render is the speaker's pull: the frames it is about to send to the card,
// starting at the frame index from.
//
// The placement is what answers, and it answers with the audio for those
// frames. Nothing is compared against a clock here: the frame index is the
// timeline, and the audio was placed on it when it arrived.
func (c *Client) Render(from uint64, out []int16) {
	c.place.Render(from, out)
}

// Active reports whether Sendspin has audio queued: the speaker's cue that
// the music plane is taken.
func (c *Client) Active() bool { return c.place.queuedMs() > 0 }

// Buffered is how much audio is queued and not yet played.
func (c *Client) Buffered() time.Duration {
	return time.Duration(c.place.queuedMs()) * time.Millisecond
}

// SetPlacement tells the placement where the speaker is and how long it
// takes to be heard.
//
// The frame counter and the latency come from one reading of the card, so
// they always belong together: the frame that is going to the card and the
// moment it will be heard.
func (c *Client) SetPlacement(position func() (uint64, time.Duration), latency func() time.Duration) {
	c.place.position = position
	c.place.latency = latency
}

// SyncDiag takes the connected server's time-exchange summary since the last
// call; false with no server connected.
func (c *Client) SyncDiag() (SyncDiag, bool) {
	c.mu.Lock()
	s := c.admitted
	c.mu.Unlock()
	if s == nil {
		return SyncDiag{}, false
	}
	return s.filter.takeDiag(), true
}

func (c *Client) hello() clientHello {
	return clientHello{
		Name:           c.cfg.Name,
		SupportedRoles: []string{rolePlayer, roleMetadata, roleController},
		DeviceInfo: &deviceInfo{
			ProductName:     c.cfg.Product,
			Manufacturer:    "Home Panel",
			SoftwareVersion: c.cfg.Version,
		},
		PlayerSupport: &playerSupport{
			SupportedFormats: []audioFormat{{Codec: "flac", Channels: outChannels, SampleRate: outRate, BitDepth: outBits}},
			BufferCapacity:   bufferCapacity,
		},
		SupportedPairMethods: pairMethods{
			PairingPSK: &pairMethod{Locations: []string{"home_assistant"}},
		},
		UnpairedAccess: unpairedAccess{Enabled: c.unpairedAccess()},
	}
}

// bufferCapacity caps the compressed bytes a server may have outstanding.
//
// The cap is on the BYTES ON THE WIRE, not on the audio they decode to: the
// spec counts each chunk's encoded size, so a server may hold
// bufferCapacity bytes of compressed audio outstanding at once.
//
// FLAC compresses roughly 2:1, so 288000 bytes of FLAC is about 3 seconds
// of stereo 48 kHz audio, and the server will queue that much. Sizing the cap
// as if the bytes were PCM (which is what "six seconds of PCM" means)
// doubles the queue, and the queue then overruns the player's own limit and
// is thrown away — which is silence.
//
// So the cap is set in wire bytes for the codec that is actually sent, and
// the player's own limit is kept above what the server can queue.
const bufferCapacity = 6 * outRate * outChannels * outBits / 8 / 2

// maxBufferedFrames is how much decoded audio the player keeps queued.
//
// It is above the 30 seconds a server may hold outstanding (the spec's own
// ceiling on a player's outstanding duration), so the server's cap always
// binds first. A queue that never overruns is never thrown away, and a
// thrown-away queue is a gap in the music.
const maxBufferedFrames = outRate * 32

// ── admission ──────────────────────────────────────────────────────────────
//
// One admitted connection at a time, ranked by what it declares: playback
// beats pairing beats nothing. An equal or higher newcomer displaces the
// holder, except that a pairing in progress is never displaced, and between
// two that declare nothing the last server to have played wins.

func (c *Client) admit(s *session, r int) bool {
	c.mu.Lock()
	cur := c.admitted
	curRank := c.rankOf[cur]
	ok := true
	switch {
	case cur == nil || cur == s:
	case curRank == 1 && cur.stagedPSK != nil:
		ok = false
	case r == 0 && curRank == 0:
		last := c.store.lastPlayback()
		ok = s.serverID == last && cur.serverID != last
	default:
		ok = r >= curRank
	}
	if ok {
		c.admitted = s
		c.rankOf[s] = r
	}
	c.mu.Unlock()
	if ok {
		c.place.use(s.filter)
	}
	if ok && cur != nil && cur != s {
		cur.goodbye("another_server")
		cur.ws.Close()
		c.place.flush()
	}
	return ok
}

func (c *Client) rerank(s *session, r int) {
	c.mu.Lock()
	c.rankOf[s] = r
	c.mu.Unlock()
}

func (c *Client) isAdmitted(s *session) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.admitted == s
}

// inUse says whether a pairing record backs a live connection, so eviction
// never removes one.
func (c *Client) inUse(serverID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for s := range c.sessions {
		if s.serverID == serverID {
			return true
		}
	}
	return false
}

func (c *Client) release(s *session) {
	c.mu.Lock()
	delete(c.sessions, s)
	delete(c.rankOf, s)
	was := c.admitted == s
	if was {
		c.admitted = nil
	}
	c.mu.Unlock()
	if was {
		c.place.flush()
		log.Printf("[sendspin] %q disconnected", s.name())
		c.changed()
	}
}

// ── status ─────────────────────────────────────────────────────────────────

// Status is what the controller shows. No secrets.
type Status struct {
	ClientID   string      `json:"clientId"`
	State      string      `json:"state"` // listening | connected | playing | busy
	Server     string      `json:"server,omitempty"`
	Paired     bool        `json:"paired"`
	PairedWith int         `json:"pairedWith"`
	Group      string      `json:"group,omitempty"`
	Synced     bool        `json:"synced"`
	SyncErrUs  int64       `json:"syncErrUs,omitempty"`
	BufferedMs int64       `json:"bufferedMs"`
	Volume     int         `json:"volume"`
	Muted      bool        `json:"muted"`
	DelayMs    int         `json:"delayMs"`
	Unpaired   bool        `json:"unpairedAccess"`
	Player     playerStats `json:"player"`
	// Metadata is what is playing.
	//
	// It is empty when nothing is playing, and the panel shows nothing
	// rather than an empty box.
	Metadata Metadata `json:"metadata"`
	// Transport is what the transport can do.
	//
	// It is empty when there is no server, and the panel shows no
	// buttons rather than buttons that do nothing.
	Transport []string `json:"transport,omitempty"`
}

// playerStats are the counts since the stream started, for the status
// report.
//
// The names are the ones the panel already reads, so the panel does not
// have to know that the placement replaced the player.
type playerStats struct {
	// Chunks is how many chunks have arrived and Samples how many frames
	// they carried.
	Chunks  int   `json:"chunks"`
	Samples int64 `json:"samples"`
	// MaxChunk is the largest chunk that has arrived, in frames.
	MaxChunk int `json:"maxChunk"`
	// LateDrops is how many chunks arrived after their frames had
	// played, and Backwards how many times the server's timestamps went
	// backwards.
	LateDrops int `json:"lateDrops"`
	Backwards int `json:"backwards"`
	// NotDue is how many periods the player found the queue was not due
	// to play yet, and TooLate how many found it already overdue.
	NotDue  int `json:"notDue"`
	TooLate int `json:"tooLate"`
	// Empty is how many periods found nothing queued at all.
	Empty int `json:"empty"`
	// Played is how many periods were filled and Frames how many frames
	// of audio they carried.
	Played int   `json:"played"`
	Frames int64 `json:"frames"`
	// LeadUs is how far ahead of the play position the head of the queue
	// is.
	LeadUs int64 `json:"leadUs"`
	// SpanUs is the time the queued audio covers, from head to tail.
	SpanUs int64 `json:"spanUs"`
	// HeadAgeUs is the server's now minus the head chunk's timestamp.
	HeadAgeUs int64 `json:"headAgeUs"`
	// TailAgeUs is the same for the newest chunk.
	TailAgeUs int64 `json:"tailAgeUs"`
	// OffsetUs is the time filter's offset and DriftPpm its drift.
	OffsetUs int64   `json:"offsetUs"`
	DriftPpm float64 `json:"driftPpm"`
	// RMS is how loud the last period handed to the speaker was. Zero
	// means the period was silence, whatever the counts say.
	RMS float64 `json:"rms"`
	// Underruns, LateDrops, Snaps, Corrections, Overruns and LastErrorUs
	// are kept so the panel's own fields still read something.
	Underruns      int `json:"underruns"`
	Snaps          int `json:"snaps"`
	Corrections    int `json:"corrections"`
	Overruns       int `json:"overruns"`
	LastErrorUs    int `json:"lastErrUs"`
	BufferOverruns int `json:"bufferOverruns"`
}

// Metadata is what the server says is playing.
//
// The progress is in milliseconds from the start of the track, and the
// duration is zero for a live stream, which has no end.
type Metadata struct {
	Title       string `json:"title,omitempty"`
	Artist      string `json:"artist,omitempty"`
	Album       string `json:"album,omitempty"`
	AlbumArtist string `json:"albumArtist,omitempty"`
	ArtworkURL  string `json:"artworkUrl,omitempty"`
	Year        int    `json:"year,omitempty"`
	Track       int    `json:"track,omitempty"`
	// ProgressMs is how far into the track the music has got.
	ProgressMs int64 `json:"progressMs,omitempty"`
	// DurationMs is how long the track is, or zero for a live stream.
	DurationMs int64 `json:"durationMs,omitempty"`
	// Speed is the playback speed as a multiplier of a thousand. A
	// thousand is normal and zero is paused.
	Speed int `json:"speed,omitempty"`
}

// Empty says whether there is anything to show.
//
// A server that sends the metadata role's object with nothing in it is
// saying that nothing is playing, and the panel shows nothing then.
func (m Metadata) Empty() bool {
	return m.Title == "" && m.Artist == "" && m.Album == "" && m.ArtworkURL == ""
}

// metadataOf turns what the wire carried into what the panel shows.
//
// The wire's names are the spec's, and the panel's are its own, so the
// two are kept apart rather than sharing a type.
func metadataOf(m streamMetadata) Metadata {
	out := Metadata{
		Title:       m.Title,
		Artist:      m.Artist,
		Album:       m.Album,
		AlbumArtist: m.AlbumArtist,
		ArtworkURL:  m.ArtworkURL,
		Year:        m.Year,
		Track:       m.Track,
	}
	if m.Progress != nil {
		out.ProgressMs = int64(m.Progress.TrackProgress)
		out.DurationMs = int64(m.Progress.TrackDuration)
		out.Speed = m.Progress.PlaybackSpeed
	}
	return out
}

func (c *Client) Status() Status {
	set := c.store.settings()
	st := Status{
		ClientID:   c.ClientID(),
		State:      "listening",
		PairedWith: len(c.store.paired()),
		Volume:     set.Volume,
		Muted:      set.Muted,
		DelayMs:    set.StaticDelayMs,
		Unpaired:   c.unpairedAccess(),
		BufferedMs: int64(c.place.queuedMs()),
		Player:     c.place.stats(),
	}
	c.mu.Lock()
	s, busy := c.admitted, c.extBusy
	c.mu.Unlock()
	if s != nil {
		st.State = "connected"
		st.Synced = s.filter.synchronized()
		if st.Synced {
			st.SyncErrUs = s.filter.errorUs()
		}
		// The filter's offset and drift, for the log: how the server's
		// clock compares with ours.
		d := s.filter.takeDiag()
		st.Player.OffsetUs = d.OffsetUs
		st.Player.DriftPpm = d.DriftPpm
		s.mu.Lock()
		st.Server = s.serverName
		st.Paired = s.psk.cat == catLongTerm
		st.Group = s.group.GroupName
		if time.Since(s.lastAudio) < 2*time.Second {
			st.State = "playing"
		}
		st.Metadata = metadataOf(s.metadata)
		if controller := s.controller; controller != nil {
			st.Transport = append([]string(nil), controller.SupportedCommands...)
		}
		s.mu.Unlock()
	}
	if busy {
		st.State = "busy"
	}
	return st
}
