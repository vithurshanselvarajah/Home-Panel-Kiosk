package fixture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/vithurshanselvarajah/Home-Panel-Kiosk/device/wakeword"
)

type Record struct {
	MelInLen int

	MelOut []float32
	Emb    []float32
	Score  float32
}

type ORT struct {
	Audio   []int16
	Records []Record

	EmbProbeIn, EmbProbeOut []float32
	ClsProbeIn              []float32
	ClsProbeOut             float32
}

const ortMagic = "OWWORT02"

func LoadORT(path string) (*ORT, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) < len(ortMagic)+4 || string(raw[:len(ortMagic)]) != ortMagic {
		return nil, fmt.Errorf("fixture %s: bad magic (want %s)", path, ortMagic)
	}

	p := len(ortMagic)

	need := func(n int) error {
		if p+n > len(raw) {
			return fmt.Errorf("fixture %s: truncated at byte %d, wanted %d more", path, p, n)
		}
		return nil
	}
	u32 := func() (int, error) {
		if err := need(4); err != nil {
			return 0, err
		}
		v := binary.LittleEndian.Uint32(raw[p:])
		p += 4
		return int(v), nil
	}
	f32n := func(n int) ([]float32, error) {
		if err := need(4 * n); err != nil {
			return nil, err
		}
		out := make([]float32, n)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[p:]))
			p += 4
		}
		return out, nil
	}

	f := &ORT{}
	nSamples, err := u32()
	if err != nil {
		return nil, err
	}
	if err := need(2 * nSamples); err != nil {
		return nil, err
	}
	f.Audio = make([]int16, nSamples)
	for i := range f.Audio {
		f.Audio[i] = int16(binary.LittleEndian.Uint16(raw[p:]))
		p += 2
	}

	nRecs, err := u32()
	if err != nil {
		return nil, err
	}
	f.Records = make([]Record, nRecs)
	for i := range f.Records {
		if f.Records[i].MelInLen, err = u32(); err != nil {
			return nil, err
		}
		frames, err := u32()
		if err != nil {
			return nil, err
		}
		if f.Records[i].MelOut, err = f32n(frames * wakeword.MelBins); err != nil {
			return nil, err
		}
		if f.Records[i].Emb, err = f32n(wakeword.FeatDim); err != nil {
			return nil, err
		}
		score, err := f32n(1)
		if err != nil {
			return nil, err
		}
		f.Records[i].Score = score[0]
	}

	if f.EmbProbeIn, err = f32n(wakeword.MelWindow * wakeword.MelBins); err != nil {
		return nil, err
	}
	if f.EmbProbeOut, err = f32n(wakeword.FeatDim); err != nil {
		return nil, err
	}
	if f.ClsProbeIn, err = f32n(wakeword.FeatWindow * wakeword.FeatDim); err != nil {
		return nil, err
	}
	clsOut, err := f32n(1)
	if err != nil {
		return nil, err
	}
	f.ClsProbeOut = clsOut[0]

	if p != len(raw) {
		return nil, fmt.Errorf("fixture %s: %d of %d bytes consumed", path, p, len(raw))
	}
	if nSamples < len(f.Records)*wakeword.ChunkSamples {
		return nil, fmt.Errorf("fixture %s: %d samples cannot supply %d chunks",
			path, nSamples, len(f.Records))
	}
	return f, nil
}

func (f *ORT) Chunk(i int) []int16 {
	return f.Audio[i*wakeword.ChunkSamples : (i+1)*wakeword.ChunkSamples]
}

const (
	RelTol = 1e-4
	AbsTol = 1e-5
)

func scaleOf(want []float32) float64 {
	var s float64
	for _, v := range want {
		if a := math.Abs(float64(v)); a > s {
			s = a
		}
	}
	return s
}

func tolerance(scale float64) float64 { return AbsTol + RelTol*scale }

func CloseEnough(got, want float32) bool {
	return math.Abs(float64(got-want)) <= tolerance(math.Abs(float64(want)))
}

type Diff struct {
	N int

	Worst   float64
	WorstAt int

	Scale float64
	Tol   float64

	Examples []string
}

func (d Diff) Ok() bool { return d.N == 0 }

func (d Diff) RelToScale() float64 {
	if d.Scale == 0 {
		return 0
	}
	return d.Worst / d.Scale
}

func Compare(got, want []float32) Diff {
	var d Diff
	if len(got) != len(want) {
		d.N = 1
		d.Examples = append(d.Examples, fmt.Sprintf("length %d, want %d", len(got), len(want)))
		return d
	}
	d.Scale = scaleOf(want)
	d.Tol = tolerance(d.Scale)
	for i := range got {
		delta := math.Abs(float64(got[i] - want[i]))
		if delta > d.Worst {
			d.Worst, d.WorstAt = delta, i
		}
		if delta > d.Tol {
			d.N++
			if len(d.Examples) < 4 {
				d.Examples = append(d.Examples, fmt.Sprintf(
					"[%d] = %v, want %v (off by %.3g, tolerance %.3g)",
					i, got[i], want[i], delta, d.Tol))
			}
		}
	}
	return d
}

type spy struct {
	inner    wakeword.Inferer
	stream   wakeword.StreamEmbedder
	melInLen []int
	melOuts  [][]float32
}

func (s *spy) Streaming() bool { return s.stream != nil && s.stream.Streaming() }
func (s *spy) ResetStream() {
	if s.stream != nil {
		s.stream.ResetStream()
	}
}
func (s *spy) EmbedStep(frames []float32) ([]float32, error) {
	if s.stream == nil {
		return nil, errors.New("fixture: engine has no streaming embedding")
	}
	return s.stream.EmbedStep(frames)
}

func StockOnly(inf wakeword.Inferer) wakeword.Inferer { return stockOnly{inf} }

type stockOnly struct{ wakeword.Inferer }

func (s *spy) Melspec(samples []float32) ([]float32, int, error) {
	out, frames, err := s.inner.Melspec(samples)
	if err == nil {
		s.melInLen = append(s.melInLen, len(samples))
		s.melOuts = append(s.melOuts, append([]float32(nil), out...))
	}
	return out, frames, err
}

func (s *spy) Embed(window []float32) ([]float32, error) { return s.inner.Embed(window) }

func (s *spy) Classify(feats []float32) (float32, error) { return s.inner.Classify(feats) }

type Report struct {
	Melspec   []Diff
	Embedding []Diff
	Score     []Diff

	ScoreFrom int

	Structural []string

	Streaming bool

	Err error
}

func (r Report) Ok() bool {
	if r.Err != nil || len(r.Structural) > 0 {
		return false
	}
	for _, set := range [][]Diff{r.Melspec, r.Embedding, r.Score} {
		for _, d := range set {
			if !d.Ok() {
				return false
			}
		}
	}
	return true
}

func (r Report) Summary() string {
	if r.Err != nil {
		return "FAIL: " + r.Err.Error()
	}
	stage := func(name string, set []Diff) string {
		bad, worst, rel, scale, tol := 0, 0.0, 0.0, 0.0, 0.0
		for _, d := range set {
			if !d.Ok() {
				bad++
			}
			if d.Worst > worst {
				worst = d.Worst
			}
			if r := d.RelToScale(); r > rel {
				rel = r
			}
			if d.Scale > scale {
				scale = d.Scale
			}
			if d.Tol > tol {
				tol = d.Tol
			}
		}
		verdict := "ok"
		if bad > 0 {
			verdict = fmt.Sprintf("FAIL (%d chunks differ)", bad)
		}

		if scale >= 1 {
			return fmt.Sprintf("  %-10s %-24s worst %.3g abs, %.3g of tensor scale\n",
				name, verdict, worst, rel)
		}
		return fmt.Sprintf("  %-10s %-24s worst %.3g abs (tolerance %.3g)\n",
			name, verdict, worst, tol)
	}
	out := stage("melspec", r.Melspec) + stage("embedding", r.Embedding) +
		stage("score", r.Score[r.ScoreFrom:])
	for _, s := range r.Structural {
		out += "  STRUCTURAL: " + s + "\n"
	}

	for name, set := range map[string][]Diff{"melspec": r.Melspec, "embedding": r.Embedding, "score": r.Score} {
		for i, d := range set {
			if !d.Ok() {
				out += fmt.Sprintf("  first %s mismatch, chunk %d: %v\n", name, i, d.Examples)
				break
			}
		}
	}
	return out
}

func Verify(inf wakeword.Inferer, fx *ORT) Report {
	s := &spy{inner: inf}
	s.stream, _ = inf.(wakeword.StreamEmbedder)
	d := wakeword.New(s)
	r := Report{ScoreFrom: wakeword.FeatWindow, Streaming: s.Streaming()}

	scores := make([]float32, len(fx.Records))
	embs := make([][]float32, len(fx.Records))
	for i := range fx.Records {
		n, err := d.Push(fx.Chunk(i))
		if err != nil {
			r.Err = fmt.Errorf("chunk %d: push: %w", i, err)
			return r
		}
		if n > 0 {
			embs[i] = d.LastEmbedding()
		}
		if n != 1 {
			r.Structural = append(r.Structural,
				fmt.Sprintf("chunk %d produced %d embeddings, want 1", i, n))
		}
		if !d.Ready() {
			continue
		}
		if scores[i], err = d.Score(); err != nil {
			r.Err = fmt.Errorf("chunk %d: score: %w", i, err)
			return r
		}
	}

	if len(s.melOuts) != len(fx.Records) {
		r.Structural = append(r.Structural,
			fmt.Sprintf("melspec ran %d times, want %d", len(s.melOuts), len(fx.Records)))
		return r
	}
	for i, rec := range fx.Records {
		if s.melInLen[i] != rec.MelInLen {
			r.Structural = append(r.Structural, fmt.Sprintf(
				"chunk %d fed melspec %d samples, Python fed %d",
				i, s.melInLen[i], rec.MelInLen))
		}
		r.Melspec = append(r.Melspec, Compare(s.melOuts[i], rec.MelOut))
		r.Embedding = append(r.Embedding, Compare(embs[i], rec.Emb))

		var d Diff
		if i >= r.ScoreFrom {
			d = Compare([]float32{scores[i]}, []float32{rec.Score})
		}
		r.Score = append(r.Score, d)
	}
	return r
}
