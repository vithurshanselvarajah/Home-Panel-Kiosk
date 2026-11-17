package scorer

import "testing"

func TestModelStemMirrorsPython(t *testing.T) {
	cases := []struct{ in, want string }{

		{"hey_mycroft_v0.1", "hey_mycroft_v0.1"},
		{"hey_jarvis_v0.1", "hey_jarvis_v0.1"},
		{"alexa_v0.1", "alexa_v0.1"},
		{"hey_clara", "hey_clara"},

		{"/app/data/oww_models/hey_clara.onnx", "hey_clara"},
		{"/app/data/oww_models/hey_clarra_v2.1.onnx", "hey_clarra_v2.1"},
		{"hey_clara.onnx", "hey_clara"},

		{"  hey_mycroft_v0.1  ", "hey_mycroft_v0.1"},
		{"", ""},
	}
	for _, c := range cases {
		if got := ModelStem(c.in); got != c.want {
			t.Errorf("ModelStem(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
