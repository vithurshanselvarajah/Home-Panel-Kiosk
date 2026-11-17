package wakeword

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
)

type record struct {
	melInLen int
	melOut   []float32
	winFNV   uint64
	emb      []float32
	featFNV  uint64
	score    float32
}

func loadFixture(t *testing.T) []record {
	t.Helper()
	raw, err := os.ReadFile("testdata/stream_fixture.bin")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if string(raw[:8]) != "OWWFIX01" {
		t.Fatalf("bad fixture magic %q", raw[:8])
	}
	p := 8
	u32 := func() int { v := binary.LittleEndian.Uint32(raw[p:]); p += 4; return int(v) }
	u64 := func() uint64 { v := binary.LittleEndian.Uint64(raw[p:]); p += 8; return v }
	f32n := func(n int) []float32 {
		out := make([]float32, n)
		for i := range out {
			out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[p:]))
			p += 4
		}
		return out
	}
	n := u32()
	recs := make([]record, n)
	for i := range recs {
		recs[i].melInLen = u32()
		frames := u32()
		recs[i].melOut = f32n(frames * MelBins)
		recs[i].winFNV = u64()
		recs[i].emb = f32n(FeatDim)
		recs[i].featFNV = u64()
		recs[i].score = f32n(1)[0]
	}
	if p != len(raw) {
		t.Fatalf("fixture not fully consumed: %d of %d bytes", p, len(raw))
	}
	return recs
}

func fnv1a(vals []float32) uint64 {
	const (
		offset = uint64(14695981039346656037)
		prime  = uint64(1099511628211)
	)
	h := offset
	var b [4]byte
	for _, v := range vals {
		binary.LittleEndian.PutUint32(b[:], math.Float32bits(v))
		for _, c := range b {
			h = (h ^ uint64(c)) * prime
		}
	}
	return h
}

type replayInferer struct {
	t    *testing.T
	recs []record
	i    int

	gotMelLen  []int
	gotWinFNV  []uint64
	gotFeatFNV []uint64
}

func (r *replayInferer) Melspec(samples []float32) ([]float32, int, error) {
	r.gotMelLen = append(r.gotMelLen, len(samples))
	rec := r.recs[r.i]
	return rec.melOut, len(rec.melOut) / MelBins, nil
}

func (r *replayInferer) Embed(window []float32) ([]float32, error) {
	r.gotWinFNV = append(r.gotWinFNV, fnv1a(window))
	return r.recs[r.i].emb, nil
}

func (r *replayInferer) Classify(feats []float32) (float32, error) {
	r.gotFeatFNV = append(r.gotFeatFNV, fnv1a(feats))
	return r.recs[r.i].score, nil
}

func TestStreamMatchesPython(t *testing.T) {
	recs := loadFixture(t)
	if len(recs) < FeatWindow+4 {
		t.Fatalf("fixture too short to exercise the feature ring: %d chunks", len(recs))
	}
	inf := &replayInferer{t: t, recs: recs}
	d := New(inf)

	audio := make([]int16, ChunkSamples)
	for i := range recs {
		inf.i = i
		n, err := d.Push(audio)
		if err != nil {
			t.Fatalf("chunk %d: Push: %v", i, err)
		}
		if n != 1 {
			t.Fatalf("chunk %d: produced %d embeddings, want exactly 1", i, n)
		}

		if i < FeatWindow {
			continue
		}
		if !d.Ready() {
			t.Fatalf("chunk %d: not Ready with %d embeddings buffered", i, len(d.feat)/FeatDim)
		}
		if _, err := d.Score(); err != nil {
			t.Fatalf("chunk %d: Score: %v", i, err)
		}
	}

	for i, got := range inf.gotMelLen {
		if want := recs[i].melInLen; got != want {
			t.Errorf("chunk %d: melspec input %d samples, want %d", i, got, want)
		}
	}

	for i, got := range inf.gotWinFNV {
		if want := recs[i].winFNV; got != want {
			t.Errorf("chunk %d: embedding window hash %016x, want %016x", i, got, want)
		}
	}

	for j, got := range inf.gotFeatFNV {
		i := j + FeatWindow
		if want := recs[i].featFNV; got != want {
			t.Errorf("chunk %d: classifier input hash %016x, want %016x", i, got, want)
		}
	}
}

func TestNotReadyBeforeSixteenEmbeddings(t *testing.T) {
	recs := loadFixture(t)
	inf := &replayInferer{t: t, recs: recs}
	d := New(inf)
	audio := make([]int16, ChunkSamples)
	for i := 0; i < FeatWindow-1; i++ {
		inf.i = i
		if _, err := d.Push(audio); err != nil {
			t.Fatal(err)
		}
		if d.Ready() {
			t.Fatalf("Ready after only %d embeddings", i+1)
		}
		if _, err := d.Score(); err != ErrNotReady {
			t.Fatalf("Score returned %v, want ErrNotReady", err)
		}
	}
	inf.i = FeatWindow - 1
	if _, err := d.Push(audio); err != nil {
		t.Fatal(err)
	}
	if !d.Ready() {
		t.Fatal("not Ready after 16 embeddings")
	}
}

type sizedInferer struct{ embeds int }

func (s *sizedInferer) Melspec(samples []float32) ([]float32, int, error) {
	frames := int(math.Ceil(float64(len(samples))/160.0 - 3.0))
	if frames < 0 {
		frames = 0
	}
	return make([]float32, frames*MelBins), frames, nil
}
func (s *sizedInferer) Embed(window []float32) ([]float32, error) {
	s.embeds++
	return make([]float32, FeatDim), nil
}
func (s *sizedInferer) Classify(feats []float32) (float32, error) { return 0, nil }

func TestSizedInfererMatchesFixtureFrameCounts(t *testing.T) {
	recs := loadFixture(t)
	var s sizedInferer
	for i, rec := range recs {
		_, frames, _ := s.Melspec(make([]float32, rec.melInLen))
		if want := len(rec.melOut) / MelBins; frames != want {
			t.Fatalf("chunk %d: %d samples -> %d frames, real model gave %d",
				i, rec.melInLen, frames, want)
		}
	}
}

func TestPartialChunksAreHeld(t *testing.T) {
	d := New(&sizedInferer{})

	for _, n := range []int{500, 500} {
		got, err := d.Push(make([]int16, n))
		if err != nil {
			t.Fatal(err)
		}
		if got != 0 {
			t.Fatalf("partial push of %d produced %d embeddings, want 0", n, got)
		}
	}
	got, err := d.Push(make([]int16, 280))
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("completing chunk produced %d embeddings, want 1", got)
	}
}

func TestOversizeChunkProducesMultipleEmbeddings(t *testing.T) {
	d := New(&sizedInferer{})
	got, err := d.Push(make([]int16, 2*ChunkSamples+37))
	if err != nil {
		t.Fatal(err)
	}
	if got != 2 {
		t.Fatalf("2.03 chunks produced %d embeddings, want 2", got)
	}

	got, err = d.Push(make([]int16, ChunkSamples-37))
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("completing the held tail produced %d embeddings, want 1", got)
	}
}

func TestResetClearsState(t *testing.T) {
	recs := loadFixture(t)
	inf := &replayInferer{t: t, recs: recs}
	d := New(inf)
	audio := make([]int16, ChunkSamples)
	for i := 0; i < FeatWindow; i++ {
		inf.i = i
		if _, err := d.Push(audio); err != nil {
			t.Fatal(err)
		}
	}
	if !d.Ready() {
		t.Fatal("expected Ready before Reset")
	}
	d.Reset()
	if d.Ready() {
		t.Fatal("Ready after Reset")
	}
	if _, err := d.Score(); err != ErrNotReady {
		t.Fatalf("Score after Reset returned %v, want ErrNotReady", err)
	}

	if len(d.mel) != MelWindow*MelBins {
		t.Fatalf("mel ring is %d values after Reset, want %d", len(d.mel), MelWindow*MelBins)
	}
	for i, v := range d.mel {
		if v != 1.0 {
			t.Fatalf("mel ring index %d is %v after Reset, want 1.0", i, v)
		}
	}
}
