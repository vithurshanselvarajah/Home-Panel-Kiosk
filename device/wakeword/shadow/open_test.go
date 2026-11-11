package shadow

import "testing"

// TestModelStemMirrorsPython pins agreement with
// controller/em_oww_models.py::prediction_key. The device looks for
// <stem>.onnx, so a stem that disagrees with the controller's means shadow mode
// silently never starts — which is exactly what happened on the first
// deployment, because filepath.Ext treats the ".1" of "hey_mycroft_v0.1" as a
// file extension.
func TestModelStemMirrorsPython(t *testing.T) {
	cases := []struct{ in, want string }{
		// Built-in names are passed through untouched. A version suffix is not
		// an extension.
		{"hey_mycroft_v0.1", "hey_mycroft_v0.1"},
		{"hey_jarvis_v0.1", "hey_jarvis_v0.1"},
		{"alexa_v0.1", "alexa_v0.1"},
		{"hey_clara", "hey_clara"},
		// A custom model is stored as a path and keys as its filename stem.
		{"/app/data/oww_models/hey_clara.onnx", "hey_clara"},
		{"/app/data/oww_models/hey_clarra_v2.1.onnx", "hey_clarra_v2.1"},
		{"hey_clara.onnx", "hey_clara"},
		// Whitespace from a hand-edited config should not produce a path with a
		// space in it.
		{"  hey_mycroft_v0.1  ", "hey_mycroft_v0.1"},
		{"", ""},
	}
	for _, c := range cases {
		if got := ModelStem(c.in); got != c.want {
			t.Errorf("ModelStem(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
