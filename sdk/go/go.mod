module github.com/ab0t-com/acp/sdk/go

go 1.23

// v0.1.0 was published with an accidental go 1.25 floor (inherited from the
// daemon module), which forced a toolchain download for normal Go versions.
// The client packages need no such floor. Use v0.1.1+.
retract v0.1.0
