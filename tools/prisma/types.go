package prisma

import (
	"bufio"
	"io"
)

const separator = "——————————————————————————————————"

type prismaInfo struct {
	version string
	problem string
}

type Config struct {
	Root      string
	PrismaDir string
	Input     io.Reader
	Output    io.Writer
	Error     io.Writer
}

type Console struct {
	root      string
	prismaDir string
	in        *bufio.Reader
	out       io.Writer
	err       io.Writer
}
