package rdp

import (
	"bytes"
	"html/template"
)

type webclientTemplateData struct {
	Title      string
	Credential string
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

var webclientTemplate = template.Must(template.New("webclient").Parse(webclientTemplateHTML))

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
