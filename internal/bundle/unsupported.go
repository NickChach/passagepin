//go:build bundled && (!linux || !amd64)

package bundle

// A bundled release must supply and validate native assets for its target.
// The first supported target is Linux amd64; fail compilation on other targets.
var _ = bundledReleasesCurrentlySupportLinuxAMD64Only
