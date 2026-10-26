module proto-distro/tools/cb_st_flasher

go 1.24.2

require (
	github.com/google/gousb v1.1.3
	github.com/urfave/cli/v2 v2.27.7
	proto-distro/tools/flashimage v0.0.0
)

require (
	github.com/cpuguy83/go-md2man/v2 v2.0.7 // indirect
	github.com/russross/blackfriday/v2 v2.1.0 // indirect
	github.com/xrash/smetrics v0.0.0-20240521201337-686a1a2994c1 // indirect
)

replace proto-distro/tools/flashimage => ../flashimage
