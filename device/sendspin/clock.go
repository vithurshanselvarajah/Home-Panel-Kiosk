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
// nowUs is the client clock, in Unix microseconds.
//
// It is what goes on the wire and what the filter is fed, so it has to be
// the same domain as the server's: a timestamp in one domain and a
// timestamp in the other differ by decades, and the difference between them
// is what the filter is supposed to measure.
func nowUs() int64 { return time.Now().UnixMicro() }
