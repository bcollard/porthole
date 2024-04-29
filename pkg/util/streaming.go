package util

import "io"

type Streamz struct {
	Input  io.Reader
	Output io.Writer
	Error  io.Writer
}
