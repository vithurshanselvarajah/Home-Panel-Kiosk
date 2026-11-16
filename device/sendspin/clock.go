package sendspin

import "time"

// epoch anchors the client clock. time.Time carries a monotonic reading, so
// Sub between two of them is immune to the wall clock being stepped — which
// the controller does to every Echo on connect (time_ms on the ack).
var epoch = time.Now()

// nowUs is the client clock, in microseconds since the epoch.
func nowUs() int64 { return time.Since(epoch).Microseconds() }

// toUs is a time on the client clock, in microseconds since the epoch.
func toUs(t time.Time) int64 { return t.Sub(epoch).Microseconds() }
