package main

import (
	"context"
	"fmt"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/mic/framework"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/audio/speaker"
	"log/slog"
	"math"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/ambient"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

type ControlPlane struct {
	mu sync.Mutex

	server *ControlServer

	advertiser *esphome.Advertiser

	listener net.Listener

	api *esphome.Server

	mediaPlayer *esphome.MediaPlayer

	camera *CameraService

	audio *deviceAudio

	wake *wakeWord

	light *ambient.Sensor

	autoBrightness bool

	lightLux float64

	luxAt        time.Time
	luxPublished bool

	brightnessCeiling int

	voice *esphome.Voice

	onSendspinStarted func()

	onWakeThreshold func(threshold float64)

	onWakeEnabled func(enabled bool)

	onAutoBrightness func(on bool)

	onDucking func(db float64)

	onSpeakerTuning  func(on bool)
	onSpeakerGain    func(db int)
	onSpeakerStereo  func(on bool)
	onSpeakerBass    func(db float64)
	onSpeakerTreble  func(db float64)
	onClearerSpeech  func(on bool)
	onLouderTop      func(on bool)
	onLouderSpeech   func(on bool)
	onMicCancel      func(on bool)
	onMicLeveling    func(on bool)
	onMicDenoise     func(on bool)
	onMicSensitivity func(db int)

	onLink func(connected bool)

	log *slog.Logger

	closeOnce sync.Once
}

func startControlPlane(
	ctx context.Context,
	log *slog.Logger,
	port int,
	key string,
	name string,
	board string,
	socket string,
	audio *deviceAudio,
	wake *wakeWord,
	light *ambient.Sensor,
) *ControlPlane {
	plane := &ControlPlane{log: log, audio: audio, wake: wake, light: light}
	if camera, err := NewCameraService(log, ""); err == nil {
		plane.camera = camera
	} else {
		log.Info("the standalone camera stream is unavailable", "reason", err)
	}

	registry := panelEntities(PanelCapabilities{
		CanControlScreen: true,
		HasBacklight:     true,
		HasSlideshow:     true,
	})
	if plane.camera != nil {
		plane.camera.AddEntities(registry)
	}

	voiceEntities(registry)

	lightEntities(registry, plane.light != nil)

	serverOptions := esphome.ServerOptions{
		Name:          name,
		FriendlyName:  board,
		MAC:           panelMAC(name),
		Version:       "0.0.3",
		EncryptionKey: key,
		Registry:      registry,
		Log:           log,
	}
	api := esphome.NewServer(serverOptions)
	plane.api = api

	voice := esphome.NewVoice(esphome.VoiceOptions{Log: log})
	api.SetVoice(voice)
	plane.voice = voice

	player := esphome.NewMediaPlayer(log)
	api.SetMediaPlayer(player)
	plane.mediaPlayer = player

	listener, err := listenForAPI(port)
	if err != nil {
		log.Error("the Home Assistant port could not be opened", "error", err)
		return plane
	}
	plane.listener = listener
	chosen := listener.Addr().(*net.TCPAddr).Port

	go func() {
		if err := api.Listen(listener); err != nil {
			log.Error("the Home Assistant API stopped", "error", err)
		}
	}()
	log.Info("the Home Assistant API is listening", "port", chosen)

	advertiser, err := esphome.Advertise(esphome.AdvertiseOptions{
		Name:         name,
		FriendlyName: board,
		MAC:          panelMAC(name),
		Port:         chosen,
		Version:      "0.0.3",
		ProjectName:  name,
		Encrypted:    key != "",
		Addresses:    panelAddresses(),
		Log:          log,
	})
	if err != nil {
		log.Warn("the device could not be announced", "error", err)
	} else {
		plane.advertiser = advertiser
	}

	var server *ControlServer
	if socket != "" {
		server = NewControlServer(ControlOptions{
			Socket:               socket,
			Registry:             registry,
			Log:                  log,
			SendspinPairingToken: audio.sendspinPairingToken,
			SetCameraPassword: func(password string) {
				if plane.camera == nil {
					return
				}
				if err := plane.camera.SetPassword(password); err != nil {
					log.Warn("the camera password was rejected", "error", err)
				}
			},
			OnCameraConnected: func(password string) {
				if plane.camera != nil {
					if err := plane.camera.SetPassword(password); err != nil {
						log.Warn("the saved camera password could not be applied", "error", err)
					}
				}
			},
		})
		if err := server.Listen(); err != nil {
			log.Error("the control socket could not be opened", "error", err)
			server = nil
		} else {
			plane.server = server
			log.Info("the control socket is open", "path", socket)
		}
	}

	api.OnConnect(func(connected bool) {
		if server != nil {
			server.SetConnected(connected)
		}

		if plane.onLink != nil {
			plane.onLink(connected)
		}
	})

	if server != nil {
		server.SetVolumeFollower(func(level int) {
			api.SetMediaPlayerVolume(float32(level) / 100)
			if plane.audio != nil {
				plane.audio.setVolume(level)
			}
		})

		server.SetBrightnessFollower(func(percent int) {
			plane.setBrightnessCeiling(percent)
		})
	}

	services := panelServices(func(command ControlCommand) {
		if server != nil {
			server.SendCommand(command)
		}
	})
	api.SetServices(services)
	wireEntities(registry, server, plane, log)

	return plane
}

func wireEntities(registry *esphome.Registry, server *ControlServer, plane *ControlPlane, log *slog.Logger) {
	send := func(command ControlCommand) {
		if server == nil {
			return
		}
		server.SendCommand(command)
	}

	for _, entity := range registry.All() {
		entity := entity
		entity.OnCommand(func(value esphome.EntityValue) {
			log.Debug("an entity was changed", "entity", entity.ObjectID, "value", value.String())
			switch entity.ObjectID {
			case "screen":

				if value.Bool {
					send(ControlCommand{Action: "wake"})
				} else {
					send(ControlCommand{Action: "screen_off"})
				}
			case "brightness":
				send(ControlCommand{Action: "brightness", Value: int(value.Number)})
			case "view":
				if value.Text == "slideshow" {
					send(ControlCommand{Action: "slideshow"})
				} else {
					send(ControlCommand{Action: "dashboard"})
				}
			case "dashboard":
				send(ControlCommand{Action: "dashboard"})
			case "display_mode":
				send(ControlCommand{Action: "display_mode", Mode: value.Text})
			case "exit":
				send(ControlCommand{Action: "exit"})
			case "wake_word_threshold":

				plane.setWakeThreshold(value.Number / 100)
			case "wake_word_enabled":
				plane.setWakeEnabled(value.Bool)
			case "music_ducking_db":
				plane.setDucking(value.Number)
			case "speaker_tuning":
				plane.setSpeakerTuning(value.Bool)
			case "speaker_gain_db":
				plane.setSpeakerGain(int(value.Number))
			case "speaker_stereo":
				plane.setSpeakerStereo(value.Bool)
			case "speaker_bass_db":
				plane.setSpeakerBass(value.Number)
			case "speaker_treble_db":
				plane.setSpeakerTreble(value.Number)
			case "speaker_clearer_speech":
				plane.setClearerSpeech(value.Bool)
			case "speaker_louder_top":
				plane.setLouderTop(value.Bool)
			case "speaker_louder_speech":
				plane.setLouderSpeech(value.Bool)
			case "mic_cancel":
				plane.setMicCancel(value.Bool)
			case "mic_leveling":
				plane.setMicLeveling(value.Bool)
			case "mic_denoise":
				plane.setMicDenoise(value.Bool)
			case "mic_sensitivity_db":
				plane.setMicSensitivity(int(value.Number))
			case "auto_brightness":
				plane.setAutoBrightness(value.Bool)
			case "camera_stream":
				if plane.camera != nil {
					if err := plane.camera.SetEnabled(value.Bool); err != nil {
						log.Warn("the camera stream could not change state", "error", err)
						if entity.Kind == esphome.KindSwitch {
							registry.SetState(entity.Key, esphome.EntityValue{Kind: esphome.KindSwitch, Bool: false})
						}
					} else {
						send(ControlCommand{Action: "setting", Setting: "camera_enabled", Value: value.Bool})
					}
				}
			case "camera_fps":
				if plane.camera != nil {
					if err := plane.camera.SetFPS(int(value.Number)); err != nil {
						log.Warn("the camera frame rate could not change", "error", err)
					}
				}
			case "camera_resolution":
				if plane.camera != nil {
					if err := plane.camera.SetResolution(value.Text); err != nil {
						log.Warn("the camera resolution could not change", "error", err)
					}
				}
			case "idle_slideshow_minutes", "slideshow_off_minutes", "touch_keyboard_enabled", "music_enabled", "music_unpaired":
				var settingValue any
				switch value.Kind {
				case esphome.KindSwitch:
					settingValue = value.Bool
				case esphome.KindNumber:
					settingValue = int(value.Number)
				}
				send(ControlCommand{Action: "setting", Setting: entity.ObjectID, Value: settingValue})
			default:
				log.Debug("an entity was changed and nothing knows what to do", "entity", entity.ObjectID)
			}
		})
	}
}

func listenForAPI(port int) (net.Listener, error) {
	if port == 0 {
		port = 6053
	}
	var lastErr error
	for attempt := 0; attempt < 100; attempt++ {
		listener, err := net.Listen("tcp", ":"+strconv.Itoa(port+attempt))
		if err == nil {
			return listener, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("home-panel: no port was free: %w", lastErr)
}

func panelAddresses() []net.IP {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	outward := defaultRouteInterface()
	var addresses []net.IP
	for _, candidate := range interfaces {
		if candidate.Flags&net.FlagUp == 0 || candidate.Flags&net.FlagLoopback != 0 {
			continue
		}

		if candidate.Flags&net.FlagPointToPoint != 0 {
			continue
		}

		if candidate.Flags&net.FlagUp != 0 && isBridge(candidate) {
			continue
		}
		if outward != "" && candidate.Name != outward {
			continue
		}
		addrs, err := candidate.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				continue
			}
			if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
				continue
			}
			addresses = append(addresses, ip)
		}
	}
	return addresses
}

func defaultRouteInterface() string {
	routes, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	lines := strings.Split(string(routes), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		if fields[1] != "00000000" {
			continue
		}
		return fields[0]
	}
	return ""
}

func isBridge(candidate net.Interface) bool {
	name := candidate.Name
	return strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") ||
		strings.HasPrefix(name, "virbr") || strings.HasPrefix(name, "veth")
}

func panelMAC(name string) string {
	seed := name
	if seed == "" {
		seed = "home-panel"
	}

	parts := []string{"02", "00", "00", "00", "00", "00"}

	sum := uint32(2166136261)
	for i := 0; i < len(seed); i++ {
		sum = (sum ^ uint32(seed[i])) * 16777619
	}
	parts[3] = fmt.Sprintf("%02x", (sum>>16)&0xFF)
	parts[4] = fmt.Sprintf("%02x", (sum>>8)&0xFF)
	parts[5] = fmt.Sprintf("%02x", sum&0xFF)
	return strings.Join(parts, ":")
}

func (p *ControlPlane) SendCommand(command ControlCommand) {
	if p.server == nil {
		return
	}
	p.server.SendCommand(command)
}

func (p *ControlPlane) SendVolume(level int) {
	if p.server == nil {
		return
	}
	p.server.SendVolume(level)
}

func (p *ControlPlane) SetCameraEnabled(enabled bool) {
	if p.camera == nil {
		return
	}
	if err := p.camera.SetEnabled(enabled); err != nil {
		p.log.Warn("the saved camera preference could not be applied", "error", err)
	}
}

func (p *ControlPlane) SetMediaPlayerVolume(volume float32) {
	if p.mediaPlayer == nil {
		return
	}
	p.mediaPlayer.SetVolume(volume)
	p.api.PushMediaPlayerState()
}

func (p *ControlPlane) SetMediaPlayerState(state esphome.MediaPlayerState) {
	if p.mediaPlayer == nil {
		return
	}
	p.mediaPlayer.SetState(state)
	p.api.PushMediaPlayerState()
}

func (p *ControlPlane) OnSendspinStarted(fn func()) {
	p.onSendspinStarted = fn
}

func (p *ControlPlane) PushCapabilities() {
	if p.api == nil {
		return
	}
	p.api.PushCapabilities()
}

func (p *ControlPlane) SetSendspin(player *esphome.Sendspin) {
	if p.api == nil {
		return
	}
	p.api.SetSendspin(player)
}

func (p *ControlPlane) AnnounceFinished() {
	if p.api == nil {
		return
	}
	p.api.SendAnnounceFinished()
}

func (p *ControlPlane) OnMediaPlayer(hooks MediaPlayerHooks) {
	if p.mediaPlayer == nil {
		return
	}
	p.mediaPlayer.OnPlay(hooks.OnPlay)
	p.mediaPlayer.OnCommand(hooks.OnCommand)
	p.mediaPlayer.OnVolume(hooks.OnVolume)
	p.mediaPlayer.OnMute(hooks.OnMute)
	p.mediaPlayer.OnState(hooks.OnState)
}

type MediaPlayerHooks struct {
	OnPlay func(url string, announcement bool)

	OnCommand func(command esphome.MediaPlayerCommand)

	OnVolume func(volume float32)

	OnMute func(muted bool)

	OnState func(state esphome.MediaPlayerState)
}

func (p *ControlPlane) Transcript() string {
	if p.voice == nil {
		return ""
	}
	return p.voice.Transcript()
}

func (p *ControlPlane) ReplyPlayedOut() {
	if p.voice == nil {
		return
	}
	p.voice.ReplyPlayedOut()
}

func (p *ControlPlane) Spoken() string {
	if p.voice == nil {
		return ""
	}
	return p.voice.Spoken()
}

func (p *ControlPlane) SpokenProgress() float64 {
	if p.audio == nil {
		return 0
	}
	return p.audio.spokenProgress()
}

func (p *ControlPlane) OnLink(fn func(connected bool)) {
	p.onLink = fn
}

func (p *ControlPlane) OnTransport(fn func(command TransportCommand)) {
	if p.server == nil {
		return
	}
	p.server.SetTransportHandler(fn)
}

func (p *ControlPlane) OnMute() {
}

func (p *ControlPlane) OnVolume(fn func(level int)) {
	if p.server == nil {
		return
	}
	p.server.SetVolumeHandler(fn)
}

func (p *ControlPlane) OnListen(fn func(phrase string)) {
	if p.server == nil {
		return
	}
	p.server.SetListenHandler(fn)
}

func (p *ControlPlane) OnVoice(hooks esphome.VoiceHooks) {
	if p.voice == nil {
		return
	}
	p.voice.SetVoiceHooks(hooks)
}

func (p *ControlPlane) StartTurn(phrase string) error {
	if p.api == nil {
		return esphome.ErrNoClient
	}
	return p.api.StartTurn(phrase)
}

func (p *ControlPlane) SendVoiceAudio(pcm []byte, end bool) error {
	if p.api == nil {
		return esphome.ErrNoClient
	}
	return p.api.SendVoiceAudio(pcm, end)
}

func (p *ControlPlane) SendVoiceEnd() error {
	if p.api == nil {
		return esphome.ErrNoClient
	}
	return p.api.SendVoiceEnd()
}

func (p *ControlPlane) setWakeThreshold(threshold float64) {
	if p.onWakeThreshold != nil {
		p.onWakeThreshold(threshold)
	}
	p.publishVoiceSettings()
}

func (p *ControlPlane) PublishVoiceSettings() {
	p.publishVoiceSettings()
}

func (p *ControlPlane) publishVoiceSettings() {
	if p.server == nil || p.audio == nil {
		return
	}

	threshold := p.audio.wakeThreshold()
	if p.wake != nil {
		threshold = p.wake.Threshold()
	}
	p.server.SetVoiceSettings(
		threshold,
		p.wakeEnabled(),
		p.audio.duckLevel(),
	)
}

func (p *ControlPlane) SetAudioSettings() {
	if p.server == nil {
		return
	}
	p.server.SetAudioSettings(framework.Get(), speaker.Get())
}

func (p *ControlPlane) SetAutoBrightness(on bool) {
	p.setAutoBrightness(on)
}

func (p *ControlPlane) setAutoBrightness(on bool) {
	p.mu.Lock()
	p.autoBrightness = on
	p.mu.Unlock()

	if p.server != nil {
		p.server.SetAutoBrightness(on)
	}
	if p.onAutoBrightness != nil {
		p.onAutoBrightness(on)
	}
}

func (p *ControlPlane) SetLux(lux float64) {
	rounded := math.Round(lux)
	p.mu.Lock()
	first := !p.luxPublished
	moved := rounded != p.lightLux && math.Abs(rounded-p.lightLux) >= luxDeadband
	quiet := time.Since(p.luxAt) >= luxRefresh
	p.lightLux = rounded
	p.luxAt = time.Now()
	p.luxPublished = true
	p.mu.Unlock()
	if first || moved || quiet {
		p.server.SetLux(rounded)
	}
}

const (
	luxDeadband = 2

	luxRefresh = 30 * time.Second
)

func (p *ControlPlane) BrightnessCeiling() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.brightnessCeiling
}

func (p *ControlPlane) setBrightnessCeiling(percent int) {
	p.mu.Lock()
	p.brightnessCeiling = percent
	p.mu.Unlock()
	if p.server != nil {
		p.server.SetBrightnessCeiling(percent)
	}
}

func (p *ControlPlane) AutoBrightness() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.autoBrightness
}

func (p *ControlPlane) Lux() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lightLux
}

func (p *ControlPlane) OnAutoBrightness(fn func(on bool)) {
	p.onAutoBrightness = fn
}

func (p *ControlPlane) wakeEnabled() bool {
	if p.wake != nil {
		return p.wake.Enabled()
	}
	return p.audio.wakeEnabled()
}

func (p *ControlPlane) setWakeEnabled(enabled bool) {
	if p.onWakeEnabled != nil {
		p.onWakeEnabled(enabled)
	}
	p.publishVoiceSettings()
}

func (p *ControlPlane) setDucking(db float64) {
	if p.onDucking != nil {
		p.onDucking(db)
	}
	p.publishVoiceSettings()
}

func (p *ControlPlane) OnWakeThreshold(fn func(threshold float64)) {
	p.onWakeThreshold = fn
}

func (p *ControlPlane) OnWakeEnabled(fn func(enabled bool)) {
	p.onWakeEnabled = fn
}

func (p *ControlPlane) OnDucking(fn func(db float64)) {
	p.onDucking = fn
}

func (p *ControlPlane) OnSpeakerTuning(fn func(on bool)) { p.onSpeakerTuning = fn }

func (p *ControlPlane) OnSpeakerGain(fn func(db int)) { p.onSpeakerGain = fn }

func (p *ControlPlane) OnSpeakerStereo(fn func(on bool)) { p.onSpeakerStereo = fn }

func (p *ControlPlane) OnSpeakerBass(fn func(db float64)) { p.onSpeakerBass = fn }

func (p *ControlPlane) OnSpeakerTreble(fn func(db float64)) { p.onSpeakerTreble = fn }

func (p *ControlPlane) OnClearerSpeech(fn func(on bool)) { p.onClearerSpeech = fn }

func (p *ControlPlane) OnLouderTop(fn func(on bool)) { p.onLouderTop = fn }

func (p *ControlPlane) OnLouderSpeech(fn func(on bool)) { p.onLouderSpeech = fn }

func (p *ControlPlane) OnMicCancel(fn func(on bool)) { p.onMicCancel = fn }

func (p *ControlPlane) OnMicLeveling(fn func(on bool)) { p.onMicLeveling = fn }

func (p *ControlPlane) OnMicDenoise(fn func(on bool)) { p.onMicDenoise = fn }

func (p *ControlPlane) OnMicSensitivity(fn func(db int)) { p.onMicSensitivity = fn }

func (p *ControlPlane) setSpeakerTuning(on bool) {
	if p.onSpeakerTuning != nil {
		p.onSpeakerTuning(on)
	}
}

func (p *ControlPlane) setSpeakerGain(db int) {
	if p.onSpeakerGain != nil {
		p.onSpeakerGain(db)
	}
}

func (p *ControlPlane) setSpeakerStereo(on bool) {
	if p.onSpeakerStereo != nil {
		p.onSpeakerStereo(on)
	}
}

func (p *ControlPlane) setSpeakerBass(db float64) {
	if p.onSpeakerBass != nil {
		p.onSpeakerBass(db)
	}
}

func (p *ControlPlane) setSpeakerTreble(db float64) {
	if p.onSpeakerTreble != nil {
		p.onSpeakerTreble(db)
	}
}

func (p *ControlPlane) setClearerSpeech(on bool) {
	if p.onClearerSpeech != nil {
		p.onClearerSpeech(on)
	}
}

func (p *ControlPlane) setLouderTop(on bool) {
	if p.onLouderTop != nil {
		p.onLouderTop(on)
	}
}

func (p *ControlPlane) setLouderSpeech(on bool) {
	if p.onLouderSpeech != nil {
		p.onLouderSpeech(on)
	}
}

func (p *ControlPlane) setMicCancel(on bool) {
	if p.onMicCancel != nil {
		p.onMicCancel(on)
	}
}

func (p *ControlPlane) setMicLeveling(on bool) {
	if p.onMicLeveling != nil {
		p.onMicLeveling(on)
	}
}

func (p *ControlPlane) setMicDenoise(on bool) {
	if p.onMicDenoise != nil {
		p.onMicDenoise(on)
	}
}

func (p *ControlPlane) setMicSensitivity(db int) {
	if p.onMicSensitivity != nil {
		p.onMicSensitivity(db)
	}
}

func (p *ControlPlane) OnWakeWord(fn func(enabled bool)) {
	if p.voice == nil {
		return
	}
	p.voice.SetWakeWordCallback(fn)
}

func (p *ControlPlane) SetWakeWord(model string, ready, enabled bool) {
	if p.voice == nil {
		return
	}
	p.voice.SetWakeWord(model, ready, enabled)
}

func (p *ControlPlane) Close() {
	p.closeOnce.Do(func() {
		if p.server != nil {
			p.server.Close()
		}
		if p.advertiser != nil {
			p.advertiser.Close()
		}
		if p.listener != nil {
			p.listener.Close()
		}
		if p.camera != nil {
			p.camera.Close()
		}
	})
}

func netDial(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}
