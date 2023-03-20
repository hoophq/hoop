module github.com/runopsio/hoop/client

go 1.19

require (
	// latest version breaks when using the loader to stderr
	// update to latest version after this https://github.com/briandowns/spinner/pull/136
	github.com/briandowns/spinner v1.23.0
	github.com/creack/pty v1.1.18
	github.com/runopsio/hoop/common v0.0.0-00010101000000-000000000000
	github.com/spf13/cobra v1.5.0
	golang.org/x/term v0.0.0-20220919170432-7a66f970e087
	k8s.io/client-go v0.20.4
)

require (
	github.com/BurntSushi/toml v1.2.1 // indirect
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/aymanbagabas/go-osc52 v1.0.3 // indirect
	github.com/containerd/console v1.0.3 // indirect
	github.com/lucasb-eyer/go-colorful v1.2.0 // indirect
	github.com/mattn/go-localereader v0.0.1 // indirect
	github.com/mattn/go-runewidth v0.0.14 // indirect
	github.com/muesli/ansi v0.0.0-20211018074035-2e021307bc4b // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/muesli/reflow v0.3.0 // indirect
	github.com/rivo/uniseg v0.2.0 // indirect
	github.com/sahilm/fuzzy v0.1.0 // indirect
)

require (
	github.com/charmbracelet/bubbles v0.14.0
	github.com/charmbracelet/bubbletea v0.23.1
	github.com/charmbracelet/lipgloss v0.6.0
	github.com/fatih/color v1.7.0 // indirect
	github.com/golang/protobuf v1.5.2 // indirect
	github.com/inconshreveable/mousetrap v1.0.0 // indirect
	github.com/mattn/go-colorable v0.1.2 // indirect
	github.com/mattn/go-isatty v0.0.16 // indirect
	github.com/muesli/termenv v0.13.0
	github.com/spf13/pflag v1.0.5 // indirect
	golang.org/x/net v0.0.0-20201021035429-f5854403a974 // indirect
	golang.org/x/sys v0.0.0-20221010170243-090e33056c14 // indirect
	golang.org/x/text v0.3.7 // indirect
	google.golang.org/genproto v0.0.0-20200526211855-cb27e3aa2013 // indirect
	google.golang.org/protobuf v1.28.1 // indirect
)

replace github.com/runopsio/hoop/common => ../common
