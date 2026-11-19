package ort_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/wakeword"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/wakeword/fixture"
	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/wakeword/ort"
)

// openRuntime opens the runtime, or skips the test.
//
// The binding needs the ONNX Runtime, so a build without it has nothing to
// open. The tests that need the runtime are skipped rather than failed:
// the build without the runtime is the one that cross compiles, and it is
// checked in CI too.
func openRuntime(t *testing.T, lib string) *ort.Runtime {
	t.Helper()
	rt, err := ort.Open(lib)
	if err != nil {
		t.Skipf("the onnx runtime is not in this build: %v", err)
	}
	return rt
}

func models(t *testing.T) (lib string, m ort.Models) {
	t.Helper()
	dir := os.Getenv("EM_OWW_MODELS")
	cls := os.Getenv("EM_OWW_CLASSIFIER")
	if dir == "" || cls == "" {
		t.Skip("set EM_OWW_MODELS and EM_OWW_CLASSIFIER to run the ONNX Runtime tests")
	}

	return "onnxruntime", ort.Models{
		Melspec:         filepath.Join(dir, "melspectrogram.onnx"),
		Embedding:       filepath.Join(dir, "embedding_model.onnx"),
		Classifier:      cls,
		EmbeddingStream: os.Getenv("EM_OWW_STREAM"),
	}
}

func TestMelspectrogramMatchesTheFixture(t *testing.T) {
	fx := loadFixture(t)
	rep := fixture.Verify(fixture.StockOnly(newInferer(t)), fx)
	if rep.Err != nil {
		t.Fatalf("verify: %v", rep.Err)
	}
	for i, d := range rep.Melspec {
		if !d.Ok() {
			t.Errorf("melspec chunk %d: %d values differ, worst %.3g: %v",
				i, d.N, d.Worst, d.Examples)
		}
	}
}

func loadFixture(t *testing.T) *fixture.ORT {
	t.Helper()
	fx, err := fixture.LoadORT("../testdata/ort_fixture.bin")
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	return fx
}

func newInferer(t *testing.T) *ort.Inferer {
	t.Helper()
	lib, m := models(t)
	rt := openRuntime(t, lib)
	t.Logf("onnxruntime %s", rt.Version())
	inf, err := rt.NewInferer(m, ort.DefaultOptions())
	if err != nil {
		t.Fatalf("NewInferer: %v", err)
	}
	t.Cleanup(func() { inf.Close() })
	t.Logf("xnnpack active: %v, streaming embedding: %s", inf.XNNPACKActive(), inf.StreamStatus())
	return inf
}

func newStreamingInferer(t *testing.T) *ort.Inferer {
	t.Helper()
	inf := newInferer(t)
	if os.Getenv("EM_OWW_STREAM") == "" {
		t.Skip("set EM_OWW_STREAM to run the streaming embedding tests")
	}
	if !inf.Streaming() {
		t.Fatalf("streaming embedding model did not come up: %s", inf.StreamStatus())
	}
	return inf
}

func TestInfererMatchesPython(t *testing.T) {
	fx := loadFixture(t)
	checkAgainstPython(t, fx, fixture.Verify(fixture.StockOnly(newInferer(t)), fx), false)
}

func TestStreamingInfererMatchesPython(t *testing.T) {
	fx := loadFixture(t)
	checkAgainstPython(t, fx, fixture.Verify(newStreamingInferer(t), fx), true)
}

func checkAgainstPython(t *testing.T, fx *fixture.ORT, rep fixture.Report, streaming bool) {
	t.Helper()
	if rep.Streaming != streaming {
		t.Fatalf("embeddings came from the streaming model: %v, want %v", rep.Streaming, streaming)
	}

	if rep.Err != nil {
		t.Fatalf("verify: %v", rep.Err)
	}
	for _, msg := range rep.Structural {
		t.Error(msg)
	}

	for i := range fx.Records {
		if d := rep.Melspec[i]; !d.Ok() {
			t.Errorf("melspec chunk %d: %d values differ, worst %.3g at %d: %v",
				i, d.N, d.Worst, d.WorstAt, d.Examples)
		}
		if d := rep.Embedding[i]; !d.Ok() {
			t.Errorf("embedding chunk %d: %d values differ, worst %.3g: %v",
				i, d.N, d.Worst, d.Examples)
		}
		if i >= rep.ScoreFrom {
			if d := rep.Score[i]; !d.Ok() {
				t.Errorf("score chunk %d: %v", i, d.Examples)
			}
		}
	}
	if !rep.Ok() {
		t.Logf("report:\n%s", rep.Summary())
	}
}

func TestProbeTensorsMatchPython(t *testing.T) {
	fx := loadFixture(t)
	inf := newInferer(t)

	emb, err := inf.Embed(fx.EmbProbeIn)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if d := fixture.Compare(emb, fx.EmbProbeOut); !d.Ok() {
		t.Errorf("embedding probe: %d values differ, worst %.3g: %v", d.N, d.Worst, d.Examples)
	}

	score, err := inf.Classify(fx.ClsProbeIn)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if fx.ClsProbeOut == 0 {
		t.Fatal("fixture probe score is zero, which would make this test vacuous")
	}
	if !fixture.CloseEnough(score, fx.ClsProbeOut) {
		t.Errorf("classifier probe = %v, Python gave %v", score, fx.ClsProbeOut)
	}
}

func TestRejectsWrongTensorSizes(t *testing.T) {
	inf := newInferer(t)
	if _, err := inf.Embed(make([]float32, wakeword.MelWindow*wakeword.MelBins-1)); err == nil {
		t.Error("Embed accepted a short window")
	}
	if _, err := inf.Classify(make([]float32, wakeword.FeatWindow*wakeword.FeatDim+1)); err == nil {
		t.Error("Classify accepted an oversized feature tensor")
	}
	if _, _, err := inf.Melspec(nil); err == nil {
		t.Error("Melspec accepted no samples")
	}
}

func TestUseAfterCloseFails(t *testing.T) {
	lib, m := models(t)
	rt := openRuntime(t, lib)
	inf, err := rt.NewInferer(m, ort.DefaultOptions())
	if err != nil {
		t.Fatalf("NewInferer: %v", err)
	}
	if err := inf.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := inf.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := inf.Embed(make([]float32, wakeword.MelWindow*wakeword.MelBins)); !errors.Is(err, ort.ErrClosed) {
		t.Errorf("Embed after Close returned %v, want ErrClosed", err)
	}
	if _, err := inf.Classify(make([]float32, wakeword.FeatWindow*wakeword.FeatDim)); !errors.Is(err, ort.ErrClosed) {
		t.Errorf("Classify after Close returned %v, want ErrClosed", err)
	}
}

func TestOpenTwiceReturnsTheSameRuntime(t *testing.T) {
	first := openRuntime(t, "onnxruntime")
	second := openRuntime(t, "onnxruntime")
	if first != second {
		t.Error("opening the same runtime twice gave two runtimes")
	}
}

func TestThreadsMustBePositive(t *testing.T) {
	lib, m := models(t)
	rt := openRuntime(t, lib)
	o := ort.DefaultOptions()
	o.Threads = 0
	if _, err := rt.NewInferer(m, o); err == nil {
		t.Error("NewInferer accepted Threads=0")
	}
}

func TestStreamStepsEqualStockWindows(t *testing.T) {
	inf := newStreamingInferer(t)
	const step, win = wakeword.MelStep * wakeword.MelBins, wakeword.MelWindow * wakeword.MelBins
	mel := make([]float32, 200*step)
	seed := uint64(7)
	for i := range mel {
		seed = seed*6364136223846793005 + 1442695040888963407
		mel[i] = float32((seed>>33)%2000000)/1000000*8 - 8
	}
	for _, from := range []int{0, 37} {
		inf.ResetStream()
		for j := from; j < 200; j++ {
			got, err := inf.EmbedStep(mel[j*step : (j+1)*step])
			if err != nil {
				t.Fatalf("step %d: %v", j, err)
			}
			end := (j + 1) * step
			if end-win < from*step {
				continue
			}
			got = append([]float32(nil), got...)
			want, err := inf.Embed(mel[end-win : end])
			if err != nil {
				t.Fatal(err)
			}
			if d := fixture.Compare(got, want); !d.Ok() {
				t.Fatalf("start %d step %d: %d values differ, worst %.3g: %v", from, j, d.N, d.Worst, d.Examples)
			}
		}
	}
}

func TestAStreamModelThatIsNotOneIsRefused(t *testing.T) {
	lib, m := models(t)
	rt := openRuntime(t, lib)
	for _, wrong := range []string{m.Embedding, m.Classifier, filepath.Join(t.TempDir(), "absent.onnx")} {
		m.EmbeddingStream = wrong
		inf, err := rt.NewInferer(m, ort.DefaultOptions())
		if err != nil {
			t.Fatalf("a bad streaming model failed the Inferer: %v", err)
		}
		if inf.Streaming() {
			t.Errorf("%s was accepted as a streaming model", filepath.Base(wrong))
		}
		if st := inf.StreamStatus(); st == "" || st == "on" {
			t.Errorf("%s: status %q does not say why", filepath.Base(wrong), st)
		}
		if _, err := inf.EmbedStep(make([]float32, wakeword.MelStep*wakeword.MelBins)); err == nil {
			t.Errorf("EmbedStep worked with no streaming model")
		}
		inf.Close()
	}
}

func TestAWrongStreamModelFailsTheSelfCheck(t *testing.T) {
	lib, m := models(t)
	wrong := os.Getenv("EM_OWW_STREAM_WRONG")
	if wrong == "" {
		t.Skip("set EM_OWW_STREAM_WRONG to a perturbed streaming model")
	}
	rt := openRuntime(t, lib)
	m.EmbeddingStream = wrong
	inf, err := rt.NewInferer(m, ort.DefaultOptions())
	if err != nil {
		t.Fatalf("a wrong streaming model failed the Inferer: %v", err)
	}
	defer inf.Close()
	if inf.Streaming() {
		t.Fatal("a streaming model that disagrees with the stock one was accepted")
	}
	if st := inf.StreamStatus(); !strings.HasPrefix(st, "self-check:") {
		t.Fatalf("status %q, want the self-check's reason", st)
	}

	if rep := fixture.Verify(inf, loadFixture(t)); !rep.Ok() || rep.Streaming {
		t.Fatalf("stock path after a refused streaming model: ok=%v streaming=%v", rep.Ok(), rep.Streaming)
	}
}
