package vips

import "runtime/debug"

// PackageVersion is the version of this package, without a leading v.
//
// It is written here as well as in the git tag because a tag is not readable
// from inside a running program: a service that wants to log or report which
// binding it is using cannot ask git. The two are kept from drifting by CI,
// which fails a tagged build whose tag does not match this constant — a version
// file that can quietly disagree with the tag is worse than no version file.
//
// Version reports the libvips this is linked against, which is a different
// question and usually the more interesting one.
const PackageVersion = "0.3.0"

// ModuleVersion reports the version the calling program actually resolved from
// the module graph, such as "v0.3.0".
//
// This is the honest answer where PackageVersion is only the intended one: a
// consumer pinned to an older release, or to a commit between releases, gets
// what they really built against. It is empty when there is no module
// information — building from a checkout of this repository, mainly, where the
// answer would be "(devel)" and mean nothing.
func ModuleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, dep := range info.Deps {
		if dep.Path == modulePath {
			if dep.Replace != nil {
				return dep.Replace.Version
			}
			return dep.Version
		}
	}
	// Built as the main module rather than as a dependency.
	if info.Main.Path == modulePath && info.Main.Version != "" &&
		info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return ""
}

const modulePath = "github.com/kirklin/vipsx"
