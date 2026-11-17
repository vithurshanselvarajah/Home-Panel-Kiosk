package sendspin

import "time"

// The clock the Sendspin protocol runs on.
//
// Every timestamp in the protocol is Unix microseconds: the client's
// transmitted time, the server's received and transmitted times, and the
// timestamps on the audio chunks. The server's clock and ours differ by
// whatever their clocks differ by, and the time filter works that
// difference out.
//
// So the clock here is Unix microseconds, and the process-relative one is
// kept for the places that measure elapsed time rather than a moment.
var epoch = time.Now()

// nowUs is the client clock, in Unix microseconds.
//
// It is what goes on the wire and what the filter is fed, so it has to be
// the same domain as the server's: a timestamp in one domain and a
// timestamp in the other differ by decades, and the difference between them
// is what the filter is supposed to measure.
func nowUs() int64 { return time.Now().UnixMicro() }

// sinceUs is how long ago a moment on the client clock was, in
// microseconds.
//
// It is the elapsed time, so it is measured against the process start: the
// difference between two Unix timestamps is the same either way, but this
// is what the scheduler and the queues are measured in.
func sinceUs(at time.Time) int64 { return time.Since(at).Microseconds() }

// toUs is a moment on the client clock, in Unix microseconds.
func toUs(t time.Time) int64 { return t.UnixMicro() }
