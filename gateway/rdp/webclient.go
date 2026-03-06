package rdp

import (
	"bytes"
	"html/template"
)

type webclientTemplateData struct {
	Title      string
	Credential string
}

type replayWebclientTemplateData struct {
	Title     string
	SessionID string
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
		initializeApp("{{.Credential}}");
	</script>
</body>
</html>`

const replayWebclientTemplateHTML = `
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
		// Replay mode - session_id will be read from URL params
		initializeApp("");
	</script>
</body>
</html>`

var webclientTemplate = template.Must(template.New("webclient").Parse(webclientTemplateHTML))
var replayWebclientTemplate = template.Must(template.New("replay-webclient").Parse(replayWebclientTemplateHTML))

func renderWebClientTemplate(title string, credential string) string {
	// Apply template and return, assume no errors since the template is static
	data := webclientTemplateData{
		Title:      title,
		Credential: credential,
	}
	var buf bytes.Buffer
	if err := webclientTemplate.Execute(&buf, data); err != nil {
		// Template is static; errors are unexpected. Return empty string on error.
		return ""
	}
	return buf.String()
}

func renderReplayWebClientTemplate(title string, sessionID string) string {
	data := replayWebclientTemplateData{
		Title:     title,
		SessionID: sessionID,
	}
	var buf bytes.Buffer
	if err := replayWebclientTemplate.Execute(&buf, data); err != nil {
		return ""
	}
	return buf.String()
}
