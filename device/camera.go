package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v4"
	"github.com/bluenviron/gortsplib/v4/pkg/auth"
	"github.com/bluenviron/gortsplib/v4/pkg/base"
	"github.com/bluenviron/gortsplib/v4/pkg/description"
	"github.com/bluenviron/gortsplib/v4/pkg/format"
	"github.com/bluenviron/gortsplib/v4/pkg/liberrors"
	"github.com/pion/rtp"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/esphome"
)

const cameraRTSPPort = 8554

var cameraModePattern = regexp.MustCompile(`([0-9]{2,5})x([0-9]{2,5})`)

// cameraInput describes one usable Linux camera and the compressed modes it
// reports. Prefer MJPEG input because it avoids moving uncompressed frames
// over USB before the one H.264 encode.
type cameraInput struct {
	device     string
	name       string
	input      string
	modes      []string
	modeInputs map[string]string
	maxMode    string
	ffmpegPath string
}

func discoverCamera(ffmpeg string) (*cameraInput, error) {
	if ffmpeg == "" {
		var err error
		ffmpeg, err = exec.LookPath("ffmpeg")
		if err != nil {
			return nil, fmt.Errorf("ffmpeg is required for the camera stream")
		}
	}
	for index := 0; index < 64; index++ {
		device := fmt.Sprintf("/dev/video%d", index)
		if _, err := os.Stat(device); err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		output, _ := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-f", "v4l2", "-list_formats", "all", "-i", device).CombinedOutput()
		cancel()
		text := string(output)
		// The list-formats command exits with an error after printing the
		// camera's capabilities. Use the format section rather than treating
		// that expected probe exit as a failed camera.
		modeInputs := make(map[string]string)
		var modes []string
		for _, line := range strings.Split(text, "\n") {
			line = strings.ToLower(line)
			input := ""
			if strings.Contains(line, "compressed") && strings.Contains(line, "mjpeg") {
				input = "mjpeg"
			} else if strings.Contains(line, "raw") && strings.Contains(line, "yuyv") {
				input = "yuyv422"
			}
			if input != "" {
				for _, mode := range parseCameraModes(line) {
					if _, exists := modeInputs[mode]; !exists || input == "mjpeg" {
						modeInputs[mode] = input
					}
				}
			}
		}
		for mode := range modeInputs {
			modes = append(modes, mode)
		}
		sort.Slice(modes, func(i, j int) bool {
			wi, hi := splitCameraMode(modes[i])
			wj, hj := splitCameraMode(modes[j])
			if wi*hi != wj*hj {
				return wi*hi < wj*hj
			}
			return wi < wj
		})
		if len(modes) == 0 {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(device, "/dev/"))
		if data, err := os.ReadFile(filepath.Join("/sys/class/video4linux", filepath.Base(device), "name")); err == nil {
			name = strings.TrimSpace(string(data))
		}
		return &cameraInput{device: device, name: name, modes: modes, modeInputs: modeInputs, maxMode: modes[len(modes)-1], ffmpegPath: ffmpeg}, nil
	}
	return nil, fmt.Errorf("no usable V4L2 camera was found")
}

func parseCameraModes(line string) []string {
	seen := make(map[string]bool)
	var modes []string
	for _, pair := range cameraModePattern.FindAllStringSubmatch(line, -1) {
		w, _ := strconv.Atoi(pair[1])
		h, _ := strconv.Atoi(pair[2])
		if w < 1 || h < 1 || w > 16384 || h > 16384 {
			continue
		}
		mode := fmt.Sprintf("%dx%d", w, h)
		if !seen[mode] {
			seen[mode] = true
			modes = append(modes, mode)
		}
	}
	return modes
}

func splitCameraMode(mode string) (int, int) {
	parts := strings.SplitN(mode, "x", 2)
	w, _ := strconv.Atoi(parts[0])
	h, _ := strconv.Atoi(parts[1])
	return w, h
}

type cameraStreamHandler struct {
	server       *gortsplib.Server
	mu           sync.RWMutex
	stream       *gortsplib.ServerStream
	publisher    *gortsplib.ServerSession
	readPassword func() string
	writeUser    string
	writePass    string
}

// CameraService owns the one camera stream exposed to Home Assistant. The
// RTSP server stays light while the camera switch is off; enabling it starts
// one capture and one H.264 encoder for every reader to share.
type CameraService struct {
	mu            sync.Mutex
	input         *cameraInput
	handler       *cameraStreamHandler
	server        *gortsplib.Server
	registry      *esphome.Registry
	enabled       *esphome.Entity
	fps           *esphome.Entity
	resolution    *esphome.Entity
	logger        *slog.Logger
	password      string
	fpsValue      int
	modeValue     string
	encoder       string
	encoderDevice string
	command       *exec.Cmd
	commandDone   chan struct{}
	closing       bool
}

type cameraProcessOutput struct{ data []byte }

func (b *cameraProcessOutput) Write(data []byte) (int, error) {
	const limit = 16 * 1024
	length := len(data)
	if len(b.data) < limit {
		remaining := limit - len(b.data)
		if len(data) > remaining {
			data = data[:remaining]
		}
		b.data = append(b.data, data...)
	}
	return length, nil
}

// NewCameraService discovers the machine's default V4L2 capture device and
// starts an RTSP server. No service or settings entities are made when the
// machine has no usable camera.
func NewCameraService(log *slog.Logger, ffmpeg string) (*CameraService, error) {
	input, err := discoverCamera(ffmpeg)
	if err != nil {
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	s := &CameraService{input: input, logger: log, fpsValue: 5, modeValue: input.maxMode}
	h := &cameraStreamHandler{readPassword: func() string {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.password
	}}
	internal := make([]byte, 24)
	if _, err := rand.Read(internal); err != nil {
		return nil, fmt.Errorf("camera: generate encoder credential: %w", err)
	}
	h.writeUser = "homepanel-encoder"
	h.writePass = hex.EncodeToString(internal)
	s.handler = h
	s.server = &gortsplib.Server{
		Handler:     h,
		RTSPAddress: fmt.Sprintf(":%d", cameraRTSPPort),
		AuthMethods: []auth.VerifyMethod{auth.VerifyMethodDigestMD5},
		// A 2560×1472 H.264 keyframe is a burst of a few hundred RTP
		// packets. Leave room for that burst so a reader on a small display
		// does not lose the entire keyframe while its socket drains.
		WriteQueueSize: 1024,
	}
	h.server = s.server
	if err := s.server.Start(); err != nil {
		return nil, fmt.Errorf("camera: RTSP listener could not start on port %d: %w", cameraRTSPPort, err)
	}
	s.encoder, s.encoderDevice = s.selectEncoder()
	log.Info("the camera RTSP server is ready", "device", input.device, "name", input.name, "maximum", input.maxMode, "encoder", s.encoder, "port", cameraRTSPPort)
	return s, nil
}

// AddEntities adds the HA controls for this camera. They exist only when a
// usable camera was discovered.
func (s *CameraService) AddEntities(registry *esphome.Registry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registry = registry
	s.enabled = registry.Add(&esphome.Entity{
		Kind: esphome.KindSwitch, Name: "Camera stream", ObjectID: "camera_stream",
		Icon: "mdi:video", State: esphome.EntityValue{Kind: esphome.KindSwitch, Bool: false},
	})
	s.fps = registry.Add(&esphome.Entity{
		Kind: esphome.KindNumber, Name: "Camera · Frame rate", ObjectID: "camera_fps",
		Icon: "mdi:video-frame-rate", Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		Min: 1, Max: 30, Step: 1,
		State: esphome.EntityValue{Kind: esphome.KindNumber, Number: float64(s.fpsValue)},
	})
	options := append([]string{"Maximum"}, s.input.modes...)
	s.resolution = registry.Add(&esphome.Entity{
		Kind: esphome.KindSelect, Name: "Camera · Resolution", ObjectID: "camera_resolution",
		Icon: "mdi:aspect-ratio", Category: esphome.EntityCategory_ENTITY_CATEGORY_CONFIG,
		Options: options,
		State:   esphome.EntityValue{Kind: esphome.KindSelect, Text: "Maximum"},
	})
}

// SetPassword applies the stream password without returning or logging it.
// An enabled stream is restarted so that existing RTSP sessions are dropped.
func (s *CameraService) SetPassword(password string) error {
	password = strings.TrimSpace(password)
	if password != "" && (len(password) < 8 || len(password) > 128) {
		return fmt.Errorf("the camera password must contain 8 to 128 characters")
	}
	s.mu.Lock()
	if s.password == password {
		s.mu.Unlock()
		return nil
	}
	s.password = password
	enabled := s.enabled != nil && s.enabled.StateOf().Bool
	var done <-chan struct{}
	if enabled {
		done = s.stopCaptureLocked()
	}
	s.mu.Unlock()
	waitCameraProcess(done)
	if enabled && password != "" {
		return s.startCapture()
	}
	return nil
}

func (s *CameraService) SetEnabled(enabled bool) error {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return fmt.Errorf("the camera stream is stopping")
	}
	if enabled && s.password == "" {
		s.mu.Unlock()
		return fmt.Errorf("set a camera password in the panel settings before enabling the stream")
	}
	if enabled == (s.command != nil) {
		s.mu.Unlock()
		return nil
	}
	if !enabled {
		done := s.stopCaptureLocked()
		s.registry.SetState(s.enabled.Key, esphome.EntityValue{Kind: esphome.KindSwitch, Bool: false})
		s.mu.Unlock()
		waitCameraProcess(done)
		return nil
	}
	s.mu.Unlock()
	return s.startCapture()
}

func (s *CameraService) SetFPS(fps int) error {
	if fps < 1 || fps > 30 {
		return fmt.Errorf("camera frame rate must be between 1 and 30")
	}
	s.mu.Lock()
	if fps == s.fpsValue {
		s.mu.Unlock()
		return nil
	}
	s.fpsValue = fps
	enabled := s.command != nil
	var done <-chan struct{}
	if enabled {
		done = s.stopCaptureLocked()
	}
	if s.fps != nil {
		s.registry.SetState(s.fps.Key, esphome.EntityValue{Kind: esphome.KindNumber, Number: float64(fps)})
	}
	s.mu.Unlock()
	if enabled {
		waitCameraProcess(done)
		return s.startCapture()
	}
	return nil
}

func (s *CameraService) SetResolution(mode string) error {
	if mode == "Maximum" {
		mode = s.input.maxMode
	}
	found := false
	for _, available := range s.input.modes {
		if mode == available {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("camera resolution %q is not supported", mode)
	}
	s.mu.Lock()
	if mode == s.modeValue {
		s.mu.Unlock()
		return nil
	}
	s.modeValue = mode
	enabled := s.command != nil
	var done <-chan struct{}
	if enabled {
		done = s.stopCaptureLocked()
	}
	if s.resolution != nil {
		s.registry.SetState(s.resolution.Key, esphome.EntityValue{Kind: esphome.KindSelect, Text: mode})
	}
	s.mu.Unlock()
	if enabled {
		waitCameraProcess(done)
		return s.startCapture()
	}
	return nil
}

func (s *CameraService) startCapture() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.command != nil {
		return nil
	}
	if s.password == "" {
		return fmt.Errorf("the camera stream has no password")
	}
	w, h := splitCameraMode(s.modeValue)
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "warning"}
	if s.encoder == "h264_vaapi" {
		args = append(args, "-vaapi_device", s.encoderDevice)
	}
	args = append(args, "-f", "v4l2")
	inputFormat := s.input.modeInputs[s.modeValue]
	if inputFormat != "" {
		args = append(args, "-input_format", inputFormat)
	}
	args = append(args, "-framerate", strconv.Itoa(s.fpsValue), "-video_size", fmt.Sprintf("%dx%d", w, h), "-i", s.input.device,
		"-an", "-c:v", s.encoder)
	switch s.encoder {
	case "h264_vaapi":
		args = append(args, "-vf", "format=nv12,hwupload", "-qp", "24", "-g", strconv.Itoa(cameraGOP(s.fpsValue)))
	case "h264_v4l2m2m":
		args = append(args, "-b:v", cameraBitrate(w, h), "-g", strconv.Itoa(cameraGOP(s.fpsValue)))
	case "h264_nvenc":
		args = append(args, "-preset", "p1", "-tune", "ull", "-profile:v", "baseline", "-bf", "0", "-b:v", cameraBitrate(w, h), "-g", strconv.Itoa(cameraGOP(s.fpsValue)))
	case "h264_qsv":
		args = append(args, "-preset", "veryfast", "-profile:v", "baseline", "-bf", "0", "-b:v", cameraBitrate(w, h), "-g", strconv.Itoa(cameraGOP(s.fpsValue)))
	default:
		args = append(args, "-preset", "ultrafast", "-tune", "zerolatency", "-profile:v", "baseline", "-pix_fmt", "yuv420p", "-b:v", cameraBitrate(w, h), "-g", strconv.Itoa(cameraGOP(s.fpsValue)))
	}
	args = append(args, "-f", "rtsp", "-rtsp_transport", "tcp", fmt.Sprintf("rtsp://%s:%s@127.0.0.1:%d/homepanel", s.handler.writeUser, s.handler.writePass, cameraRTSPPort))
	cmd := exec.Command(s.input.ffmpegPath, args...)
	cmd.Stdout = nil
	// Keep bounded FFmpeg diagnostics, then redact its publisher credentials
	// before anything reaches the app log.
	diagnostics := &cameraProcessOutput{}
	cmd.Stderr = diagnostics
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("camera encoder could not start: %w", err)
	}
	s.command = cmd
	done := make(chan struct{})
	s.commandDone = done
	if s.enabled != nil && s.registry != nil {
		s.registry.SetState(s.enabled.Key, esphome.EntityValue{Kind: esphome.KindSwitch, Bool: true})
	}
	go func(expected *exec.Cmd) {
		err := expected.Wait()
		s.mu.Lock()
		close(done)
		if s.command == expected {
			s.command = nil
			s.commandDone = nil
			if s.enabled != nil && s.registry != nil {
				s.registry.SetState(s.enabled.Key, esphome.EntityValue{Kind: esphome.KindSwitch, Bool: false})
			}
			if !s.closing && err != nil {
				detail := strings.ReplaceAll(string(diagnostics.data), s.handler.writeUser+":"+s.handler.writePass+"@", "[redacted]@")
				s.logger.Warn("the camera encoder stopped", "error", err, "details", strings.TrimSpace(detail))
			}
		}
		s.mu.Unlock()
	}(cmd)
	return nil
}

func (s *CameraService) stopCaptureLocked() <-chan struct{} {
	if s.command == nil {
		return nil
	}
	cmd := s.command
	done := s.commandDone
	s.command = nil
	s.commandDone = nil
	if cmd.Process != nil {
		_ = cmd.Process.Signal(os.Interrupt)
		go func() {
			timer := time.NewTimer(3 * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				_ = cmd.Process.Kill()
			case <-done:
			}
		}()
	}
	return done
}

func waitCameraProcess(done <-chan struct{}) {
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(4 * time.Second):
	}
}

func (s *CameraService) Close() {
	s.mu.Lock()
	s.closing = true
	done := s.stopCaptureLocked()
	server := s.server
	s.mu.Unlock()
	waitCameraProcess(done)
	if server != nil {
		server.Close()
	}
}

func (s *CameraService) selectEncoder() (string, string) {
	// VAAPI is tried at runtime, not inferred from the presence of an
	// encoder name in FFmpeg: builds list encoders even when the device has
	// no usable driver or hardware.
	if s.probeEncoder("h264_v4l2m2m", nil) {
		return "h264_v4l2m2m", ""
	}
	for _, device := range []string{"/dev/dri/renderD128", "/dev/dri/renderD129"} {
		if _, err := os.Stat(device); err != nil {
			continue
		}
		if s.probeEncoder("h264_vaapi", &device) {
			s.logger.Info("hardware video encoding is available", "encoder", "h264_vaapi", "device", device)
			return "h264_vaapi", device
		}
	}
	for _, encoder := range []string{"h264_nvenc", "h264_qsv"} {
		if s.probeEncoder(encoder, nil) {
			return encoder, ""
		}
	}
	s.logger.Warn("no working hardware H.264 encoder was found; using the low-latency software encoder")
	return "libx264", ""
}

func (s *CameraService) probeEncoder(encoder string, device *string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error"}
	if device != nil {
		args = append(args, "-init_hw_device", "vaapi=va:"+*device)
	}
	args = append(args, "-f", "lavfi", "-i", "color=size=320x240:rate=1", "-frames:v", "1")
	if encoder == "h264_vaapi" {
		args = append(args, "-vf", "format=nv12,hwupload")
	}
	args = append(args, "-c:v", encoder, "-f", "null", "-")
	if err := exec.CommandContext(ctx, s.input.ffmpegPath, args...).Run(); err != nil {
		s.logger.Debug("hardware video encoder probe failed", "encoder", encoder, "error", err)
		return false
	}
	s.logger.Info("hardware video encoding is available", "encoder", encoder)
	return true
}

func cameraBitrate(width, height int) string {
	// About 2 Mbit/s per 1080p, scaled by pixel count, with a practical
	// floor so small views remain clear.
	bitrate := width * height * 2 / (1920 * 1080)
	if bitrate < 1 {
		bitrate = 1
	}
	return fmt.Sprintf("%dM", bitrate*2)
}

func cameraGOP(fps int) int {
	// Some cameras deliver fewer frames than requested at their largest
	// mode. A one-second keyframe interval lets new RTSP readers recover
	// quickly even when the capture rate is lower than the requested rate.
	return max(1, fps/2)
}

func (h *cameraStreamHandler) authorized(conn *gortsplib.ServerConn, request *base.Request, publisher bool) bool {
	if publisher {
		return conn.VerifyCredentials(request, h.writeUser, h.writePass)
	}
	password := h.readPassword()
	return password != "" && conn.VerifyCredentials(request, "homepanel", password)
}

func (h *cameraStreamHandler) OnDescribe(ctx *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	if !h.authorized(ctx.Conn, ctx.Request, false) {
		return &base.Response{StatusCode: base.StatusUnauthorized}, nil, liberrors.ErrServerAuth{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if strings.Trim(ctx.Path, "/") != "homepanel" || h.stream == nil {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}
	return &base.Response{StatusCode: base.StatusOK}, h.stream, nil
}

func (h *cameraStreamHandler) OnAnnounce(ctx *gortsplib.ServerHandlerOnAnnounceCtx) (*base.Response, error) {
	if !h.authorized(ctx.Conn, ctx.Request, true) {
		return &base.Response{StatusCode: base.StatusUnauthorized}, liberrors.ErrServerAuth{}
	}
	if strings.Trim(ctx.Path, "/") != "homepanel" {
		return &base.Response{StatusCode: base.StatusNotFound}, nil
	}
	stream := &gortsplib.ServerStream{Server: h.server, Desc: ctx.Description}
	if err := stream.Initialize(); err != nil {
		return nil, err
	}
	h.mu.Lock()
	old := h.stream
	oldPublisher := h.publisher
	h.stream, h.publisher = stream, ctx.Session
	h.mu.Unlock()
	if old != nil {
		old.Close()
	}
	if oldPublisher != nil && oldPublisher != ctx.Session {
		oldPublisher.Close()
	}
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func (h *cameraStreamHandler) OnSetup(ctx *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	if ctx.Session.State() == gortsplib.ServerSessionStatePreRecord {
		if !h.authorized(ctx.Conn, ctx.Request, true) {
			return &base.Response{StatusCode: base.StatusUnauthorized}, nil, liberrors.ErrServerAuth{}
		}
		return &base.Response{StatusCode: base.StatusOK}, nil, nil
	}
	if !h.authorized(ctx.Conn, ctx.Request, false) {
		return &base.Response{StatusCode: base.StatusUnauthorized}, nil, liberrors.ErrServerAuth{}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if h.stream == nil {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}
	return &base.Response{StatusCode: base.StatusOK}, h.stream, nil
}

func (h *cameraStreamHandler) OnPlay(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	if !h.authorized(ctx.Conn, ctx.Request, false) {
		return &base.Response{StatusCode: base.StatusUnauthorized}, liberrors.ErrServerAuth{}
	}
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func (h *cameraStreamHandler) OnRecord(ctx *gortsplib.ServerHandlerOnRecordCtx) (*base.Response, error) {
	h.mu.RLock()
	stream, publisher := h.stream, h.publisher
	h.mu.RUnlock()
	if stream == nil || publisher != ctx.Session {
		return &base.Response{StatusCode: base.StatusNotFound}, nil
	}
	ctx.Session.OnPacketRTPAny(func(media *description.Media, _ format.Format, packet *rtp.Packet) {
		if err := stream.WritePacketRTP(media, packet); err != nil {
			slog.Debug("camera RTP frame could not be distributed", "error", err)
		}
	})
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func (h *cameraStreamHandler) OnSessionClose(ctx *gortsplib.ServerHandlerOnSessionCloseCtx) {
	h.mu.Lock()
	if h.publisher != ctx.Session {
		h.mu.Unlock()
		return
	}
	stream := h.stream
	h.stream, h.publisher = nil, nil
	h.mu.Unlock()
	if stream != nil {
		stream.Close()
	}
}
