// Package dep sits at the root of a module the main go.mod replaces with a
// directory, so its compile runs next to dep/go.mod and its `toolchain` line.
package dep

func Greeting() string { return "ok" }
