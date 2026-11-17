# Third-party components

The device program links the ONNX Runtime, which is MIT licensed (see
`COPYING`). It depends on the components below, each of which keeps its
own licence. All are permissive and compatible with redistribution under
MIT.

## Depended on

| Component | Where | Licence | Copyright |
|---|---|---|---|
| [ONNX Runtime](https://github.com/microsoft/onnxruntime) | linked into the device program | MIT | Microsoft Corporation; see adjacent `COPYING` |
| [gorilla/websocket](https://github.com/gorilla/websocket) | `go.mod` | BSD-2-Clause | The Gorilla Authors |
| [grandcat/zeroconf](https://github.com/grandcat/zeroconf) | `go.mod` | BSD-3-Clause | Frank Wang |
| [flynn/noise](https://github.com/flynn/noise) | `go.mod` | BSD-3-Clause | Prime Directive, Inc. |
| [mewkiz/flac](https://github.com/mewkiz/flac) | `go.mod` | Public domain | mewkiz |

## Related projects

The wake word is openWakeWord, which is licensed separately under
CC BY-NC-SA 4.0. The models are not redistributed here: they are
installed on the device by the person who uses the panel.
