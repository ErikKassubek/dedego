// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build mips64 || mips64le

package runtime

import "unsafe"

// Called from assembly only; declared for go vet.
func load_g()
func save_g()

//go:noescape
func asmcgocall_no_g(fn, arg unsafe.Pointer)

// getcallerfp returns the address of the frame pointer in the callers frame or 0 if not implemented.
func getcallerfp() uintptr { return 0 }
