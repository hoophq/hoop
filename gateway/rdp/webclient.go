package rdp

import (
	"bytes"
	"html/template"
)

type webclientTemplateData struct {
	Title         string
	Credential    string
	DesktopWidth  int
	DesktopHeight int
}

type rdpDesktopSize struct {
	width  int
	height int
}

func rdpDesktopSizeFromPreset(preset string) (rdpDesktopSize, bool) {
	switch preset {
	case "", "fit":
		return rdpDesktopSize{}, true
	case "1280x720":
		return rdpDesktopSize{width: 1280, height: 720}, true
	case "1366x768":
		return rdpDesktopSize{width: 1366, height: 768}, true
	case "1600x900":
		return rdpDesktopSize{width: 1600, height: 900}, true
	case "1920x1080":
		return rdpDesktopSize{width: 1920, height: 1080}, true
	case "2560x1440":
		return rdpDesktopSize{width: 2560, height: 1440}, true
	case "3840x2160":
		return rdpDesktopSize{width: 3840, height: 2160}, true
	default:
		return rdpDesktopSize{}, false
	}
}

const webclientTemplateHTML = `
<!DOCTYPE html>
<html>
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
 	<title>{{.Title}}</title>
</head>
<body>
    <div id="app">
        <canvas
        id="rdp-canvas"
        width="1280"
        height="800"
        ></canvas>
    </div>
	<script type="module">
		import "/rdpclient/index.js";
		initializeApp("{{.Credential}}", {{.DesktopWidth}}, {{.DesktopHeight}});
	</script>
</body>
</html>`

var webclientTemplate = template.Must(template.New("webclient").Parse(webclientTemplateHTML))

func renderWebClientTemplate(title string, credential string, desktopSize rdpDesktopSize) string {
	// Apply template and return, assume no errors since the template is static
	data := webclientTemplateData{
		Title:         title,
		Credential:    credential,
		DesktopWidth:  desktopSize.width,
		DesktopHeight: desktopSize.height,
	}
	var buf bytes.Buffer
	if err := webclientTemplate.Execute(&buf, data); err != nil {
		// Template is static; errors are unexpected. Return empty string on error.
		return ""
	}
	return buf.String()
}
