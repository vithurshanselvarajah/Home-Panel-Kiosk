# Notices

This directory holds code that came from the EchoMuse SDK, which is
MIT-licensed. It is kept here so the panel builds from its own tree, with no
dependency on another project's checkout.

## What came from where

| Path | From |
|---|---|
| `client.go`, `codec.go`, `keys.go`, `messages.go`, `noise.go` | `EchoMuse-SDK/clients/go/echomuse/sendspin/` |
| `outclock.go`, `player.go`, `session.go`, `store.go` | `EchoMuse-SDK/clients/go/echomuse/sendspin/` |
| `timefilter.go`, `volume.go` | `EchoMuse-SDK/clients/go/echomuse/sendspin/` |

The changes made to that code are the package path and the path of the
internal helper it writes its store through.

`sendspin_test.go` was written for this repository; the package had no tests
of its own.

## The EchoMuse licence

```
MIT License

Copyright (c) 2026 Wil Bowes

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```
