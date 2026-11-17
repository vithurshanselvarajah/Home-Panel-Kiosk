package sendspin

import (
	"context"
	"fmt"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/grandcat/zeroconf"
)

const (
	DefaultPort = 8928
	Path        = "/sendspin"
	serviceType = "_sendspin._tcp"
	maxSessions = 4
)

type Config struct {
	StorePath string
	Name      string
	Instance  string
	Port      int

	Addresses []net.IP
	Product   string
	Version   string

	Unpaired bool

	RequiredLeadTime time.Duration
	MinBuffer        time.Duration
}

type Client struct {
	cfg   Config
	store *store

	place *placement

	mu               sync.Mutex
	admitted         *session
	rankOf           map[*session]int
	sessions         map[*session]bool
	extBusy          bool
	bufferMs         int
	minBufferFloorMs int
	requiredLeadMs   int
	unpaired         bool
	onChange         func()
	onSettings       func(playerSettings)

	onVolume func(v int)

	srv  *http.Server
	mdns *zeroconf.Server
}

func New(cfg Config) (*Client, error) {
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}

	if cfg.RequiredLeadTime <= 0 {
		cfg.RequiredLeadTime = 300 * time.Millisecond
	}
	if cfg.MinBuffer <= 0 {
		cfg.MinBuffer = 200 * time.Millisecond
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

func (c *Client) ClientID() string { return c.store.id.clientID() }

func (c *Client) PairingToken() string { return c.store.token() }

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

func (c *Client) Start() error {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(c.cfg.Port))
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	up := websocket.Upgrader{

		CheckOrigin: func(*http.Request) bool { return true },
	}
	mux.HandleFunc(Path, func(w http.ResponseWriter, r *http.Request) {

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

func (c *Client) OnSettings(f func(volume int, muted bool, delayMs int)) {
	c.mu.Lock()
	c.onSettings = func(p playerSettings) { f(p.Volume, p.Muted, p.StaticDelayMs) }
	c.mu.Unlock()
}

func (c *Client) OnVolume(f func(v int)) {
	c.mu.Lock()
	c.onVolume = f
	c.mu.Unlock()
	set := c.store.settings()
	c.place.setGain(c.gainVolume(set.Volume), set.Muted)
}

func (c *Client) OnServerVolume(f func(v int)) {
	c.mu.Lock()
	c.onVolume = f
	c.mu.Unlock()
	set := c.store.settings()

	c.place.setGain(100, set.Muted)
}

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

func (c *Client) gainVolume(v int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onVolume != nil {
		return 100
	}
	return v
}

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

	return append([]string(nil), controller.SupportedCommands...)
}

func (c *Client) CanTransport(command string) bool {
	for _, name := range c.TransportCommands() {
		if name == command {
			return true
		}
	}
	return false
}

func (c *Client) Transport(command string) bool {
	return c.transport(&controllerCommand{Command: command})
}

func (c *Client) SeekTo(positionMs int64) bool {
	return c.transport(&controllerCommand{Command: "seek", PositionMs: &positionMs})
}

func (c *Client) SeekRelative(offsetMs int64) bool {
	return c.transport(&controllerCommand{Command: "seek_relative", OffsetMs: &offsetMs})
}

func (c *Client) transport(cmd *controllerCommand) bool {
	c.mu.Lock()
	s := c.admitted
	c.mu.Unlock()
	if s == nil {
		return false
	}

	s.asked.Store(cmd.Command)
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

func (c *Client) Render(from uint64, out []int16) {
	c.place.Render(from, out)
}

func (c *Client) Active() bool {
	c.mu.Lock()
	s := c.admitted
	c.mu.Unlock()
	if s == nil {
		return false
	}
	return s.playing()
}

func (c *Client) Streaming() bool {
	c.mu.Lock()
	s := c.admitted
	c.mu.Unlock()
	if s == nil {
		return false
	}
	return s.playingMusic()
}

func (c *Client) Buffered() time.Duration {
	return time.Duration(c.place.queuedMs()) * time.Millisecond
}

func (c *Client) SetBackgrounds(arbiter *speaker.Arbiter) {
	c.place.backgrounds = arbiter
}

func (c *Client) SetAttach(attach, detach func()) {
	c.place.SetAttach(attach, detach)
}

func (c *Client) SetStarted(started func() bool) {
	c.place.started = started
}

func (c *Client) SetPlacement(position func() (uint64, time.Duration), latency func() time.Duration) {
	c.place.position = position
	c.place.latency = latency
}

func (c *Client) SyncDiag() (SyncDiag, bool) {
	c.mu.Lock()
	s := c.admitted
	c.mu.Unlock()
	if s == nil {
		return SyncDiag{}, false
	}
	return s.filter.takeDiag(), true
}

func offeredFormats() []audioFormat {
	return []audioFormat{
		{Codec: "flac", Channels: outChannels, SampleRate: outRate, BitDepth: outBits},
		{Codec: "opus", Channels: outChannels, SampleRate: outRate, BitDepth: outBits},
		{Codec: "pcm", Channels: outChannels, SampleRate: outRate, BitDepth: outBits},
	}
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

			SupportedFormats: []audioFormat{
				{Codec: "flac", Channels: outChannels, SampleRate: outRate, BitDepth: outBits},
				{Codec: "opus", Channels: outChannels, SampleRate: outRate, BitDepth: outBits},
				{Codec: "pcm", Channels: outChannels, SampleRate: outRate, BitDepth: outBits},
			},
			BufferCapacity: bufferCapacity,
		},
		SupportedPairMethods: pairMethods{
			PairingPSK: &pairMethod{Locations: []string{"home_assistant"}},
		},
		UnpairedAccess: unpairedAccess{Enabled: c.unpairedAccess()},
	}
}

const bufferSeconds = 30

const bufferCapacity = bufferSeconds * outRate * outChannels * outBits / 8

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

		c.place.close()
		log.Printf("[sendspin] %q disconnected", s.name())
		c.changed()
	}
}

type Status struct {
	ClientID   string      `json:"clientId"`
	State      string      `json:"state"`
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

	Metadata Metadata `json:"metadata"`

	Transport []string `json:"transport,omitempty"`
}

type playerStats struct {
	Chunks  int   `json:"chunks"`
	Samples int64 `json:"samples"`

	MaxChunk int `json:"maxChunk"`

	LateDrops int `json:"lateDrops"`
	Backwards int `json:"backwards"`

	NotDue  int `json:"notDue"`
	TooLate int `json:"tooLate"`

	Empty int `json:"empty"`

	Played int   `json:"played"`
	Frames int64 `json:"frames"`

	LeadUs int64 `json:"leadUs"`

	SpanUs int64 `json:"spanUs"`

	HeadAgeUs int64 `json:"headAgeUs"`

	TailAgeUs int64 `json:"tailAgeUs"`

	OffsetUs int64   `json:"offsetUs"`
	DriftPpm float64 `json:"driftPpm"`

	RMS float64 `json:"rms"`

	Underruns      int `json:"underruns"`
	Snaps          int `json:"snaps"`
	Corrections    int `json:"corrections"`
	Overruns       int `json:"overruns"`
	LastErrorUs    int `json:"lastErrUs"`
	BufferOverruns int `json:"bufferOverruns"`
}

type Metadata struct {
	Title       string `json:"title,omitempty"`
	Artist      string `json:"artist,omitempty"`
	Album       string `json:"album,omitempty"`
	AlbumArtist string `json:"albumArtist,omitempty"`
	ArtworkURL  string `json:"artworkUrl,omitempty"`
	Year        int    `json:"year,omitempty"`
	Track       int    `json:"track,omitempty"`

	ProgressMs int64 `json:"progressMs,omitempty"`

	DurationMs int64 `json:"durationMs,omitempty"`

	Speed int `json:"speed,omitempty"`
}

func (m Metadata) Empty() bool {
	return m.Title == "" && m.Artist == "" && m.Album == "" && m.ArtworkURL == ""
}

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
