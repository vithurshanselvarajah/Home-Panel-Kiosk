# Adding a wake word

The panel runs wake word detection locally using [openWakeWord](https://github.com/dscripka/openWakeWord). It needs a shared feature pipeline and one classifier model. The ONNX Runtime is shipped with the device binary; it does not need a separate system install.

## Install or replace the classifier

The default model directory is `/usr/share/home-panel/wakeword`. It must contain these files:

| File | Purpose |
| --- | --- |
| `melspectrogram.onnx` | Shared audio feature model |
| `embedding_model.onnx` | Shared speech embedding model |
| `<wakeword>_v<version>.onnx` | Classifier for the phrase to detect |

The shared files come from the openWakeWord release/model bundle and are reused for every phrase. For example, `hey_jarvis_v0.1.onnx` detects “hey jarvis”. Get pre-trained models from the [openWakeWord releases](https://github.com/dscripka/openWakeWord/releases). A custom ONNX classifier can also be trained with openWakeWord; use its [custom model guide and example configuration](https://github.com/dscripka/openWakeWord/blob/main/examples/custom_model.yml).

Copy the model files to the panel and make them readable by the account that launches the app. For a model bundle in `/tmp/openwakeword-models`, the commands are:

```sh
sudo install -d -m 755 /usr/share/home-panel/wakeword
sudo install -m 644 /tmp/openwakeword-models/melspectrogram.onnx /usr/share/home-panel/wakeword/
sudo install -m 644 /tmp/openwakeword-models/embedding_model.onnx /usr/share/home-panel/wakeword/
sudo install -m 644 /tmp/openwakeword-models/hey_jarvis_v0.1.onnx /usr/share/home-panel/wakeword/
```

Replace the example classifier filename with the model you are installing. Keep exactly one classifier `.onnx` file in this directory. The device currently supports one active wake word; if multiple classifiers are present, it picks the alphabetically first and warns that the rest are ignored. The shared `melspectrogram`, `embedding_model`, and optional `embedding_stream` files are not classifiers.

Restart the Home Panel app after adding, replacing, or removing a classifier. The classifier list is read at device startup. The model filename supplies both the Home Assistant wake word ID and its displayed phrase: underscores become spaces and a trailing version such as `_v0.1` is removed. Use a descriptive filename such as `hey_computer_v1.onnx` so Home Assistant displays “hey computer”.

## Use a custom model directory

Set `HOME_PANEL_WAKEWORD_DIR` in the environment of the process that launches the app. The directory must contain the same shared models and one classifier. For example:

```sh
HOME_PANEL_WAKEWORD_DIR=/opt/home-panel/models npm start
```

For a packaged or desktop-launched app, set the variable in that launcher's environment; setting it in an unrelated terminal will not affect an already-running app.

## Confirm it loaded

At startup, check the device log for `the local wake word is ready`; it includes the selected model filename and derived phrase. If it is missing, look for `the local wake word is not available` or `a wake word model could not be loaded`. The shared models and classifier must be valid ONNX files compatible with openWakeWord's expected model inputs. Home Assistant will expose the loaded phrase in the device's wake word picker when its voice assistant connects.

If you add or replace a custom classifier, test it in the room where the panel will be used. Wake word accuracy and false activations depend on the microphone, distance, room noise, and the model's training data. Adjust the wake word threshold in Home Assistant if it is too sensitive or misses the phrase.
