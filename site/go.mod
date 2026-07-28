// This directory is a module of its own, and that is the entire point.
//
// The go command treats any directory containing a go.mod as a separate module
// and leaves it out of the parent's module zip. Everything here is a generated
// demonstration page — thirty-six images that nobody running `go get
// github.com/kirklin/vipsx` should have to download. Deleting this file would
// put roughly a megabyte of screenshots into every consumer's module cache.
//
// There is no Go code here and there is not meant to be.
module github.com/kirklin/vipsx/site

go 1.24
