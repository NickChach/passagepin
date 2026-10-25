//go:build bundled && linux && amd64

package bundle

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdlib.h>
static const char* preload_dependency(const char* path) {
    if (dlopen(path, RTLD_NOW | RTLD_GLOBAL) == NULL) return dlerror();
    return NULL;
}
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// Keep dependencies loaded for the process lifetime. Loading libgcc first and
// libstdc++ second avoids relying on a system C++ installation or on transitive
// RUNPATH lookup (which is not inherited by a library's dependencies).
func preload(paths []string) error {
	for _, path := range paths {
		cpath := C.CString(path)
		message := C.preload_dependency(cpath)
		C.free(unsafe.Pointer(cpath))
		if message != nil {
			return fmt.Errorf("load bundled dependency %s: %s", path, C.GoString(message))
		}
	}
	return nil
}
