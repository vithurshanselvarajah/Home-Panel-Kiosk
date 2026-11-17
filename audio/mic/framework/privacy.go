package framework

import "sync/atomic"

// The software cut is whether the microphone's audio is thrown away before anything reads it.
//
// A hardware mute is the board's own latch, and this is the software's half of it: the mute button
// flips the latch and the room stops reaching the application at all. The software cut is what the
// panel uses when it mutes without the latch, so that nothing downstream keeps the audio.
//
// The seed is set before the capture is opened, so that a unit whose mute is on never hands a frame
// to anybody.
var cut atomic.Bool

// Seed sets the software cut.
func Seed(muted bool) { cut.Store(muted) }

// SoftwareCut reports whether the software cut is on.
func SoftwareCut() bool { return cut.Load() }

// SetSoftwareCut turns the software cut on or off.
func SetSoftwareCut(on bool) { cut.Store(on) }
