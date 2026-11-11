# Notices

This directory holds code that came from the EchoMuse project, which is
MIT-licensed. It is kept here so the panel builds from its own tree, with no
dependency on another project's checkout.

## What came from where

| Path | From |
|---|---|
| `stream.go` | `EchoMuse/device/internal/wakeword/stream.go` |
| `ort/ort.go`, `ort/vad.go`, `ort/shim.h` | `EchoMuse/device/internal/wakeword/ort/` |
| `ort/include/onnxruntime_c_api.h` | `EchoMuse/device/internal/wakeword/ort/include/` |
| `shadow/` | `EchoMuse/device/internal/wakeword/shadow/` |

The speech detector in `device/vad/` came from the EchoMuse SDK's
`clients/go/echomuse/vad/`, and has its own notice in that directory.

The changes made to that code are the package paths, the directory the models
are read from, and the wording of the comments that referred to the device
firmware or to the controller.

## ONNX Runtime

`ort/include/onnxruntime_c_api.h` is Microsoft's ONNX Runtime C API header,
vendored under the MIT licence. Its licence is in `ort/COPYING`.

The ONNX Runtime **library** itself is not included. It is 12.3MB, it changes
far less often than the application, and it is loaded at runtime rather than
linked, so the panel runs without it and simply has no local wake word.

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
