//go:build bundled && linux && amd64

package bundle

import "embed"

const Available = true

//go:embed assets/model.onnx
var Weights []byte

//go:embed assets/vocab.txt
var Vocabulary []byte

//go:embed assets/runtime/libonnxruntime.so.1.22.0 assets/runtime/libstdc++.so.6 assets/runtime/libgcc_s.so.1 assets/manifest.json assets/licenses.txt
var embeddedPayload embed.FS

var payload = embeddedPayload
