package userapi

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/api/httputils"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
)

const (
	// maxImportBytes and maxImportRows bound a file import. A directory of
	// reviewers is far smaller; a file this size is a mistake.
	maxImportBytes = 5 << 20
	maxImportRows  = 10000
)

// ImportUsers
//
//	@Summary		Import Users
//	@Description	Create or update users and their groups from a file, for a control plane whose groups the Slack import does not manage. Send JSON, or a CSV as the multipart field "file" with the columns email,name,groups (groups separated by ";") and an optional deactivate_missing field. A bad row fails that row, not the file. Every group the file names gets exactly the users that list it; the admin group cannot be named. Control plane only.
//	@Tags			User Management
//	@Accept			json,mpfd
//	@Produce		json
//	@Param			request				body		openapi.UsersImportRequest	true	"The request body resource"
//	@Success		200					{object}	openapi.UsersImportResponse
//	@Failure		400,409,412,413,500	{object}	openapi.HTTPError
//	@Router			/users/import [post]
func ImportUsers(c *gin.Context) {
	if !appconfig.Get().IsControlPlane() {
		c.JSON(http.StatusPreconditionFailed, gin.H{"message": "the user import is served by the control plane"})
		return
	}
	ctx := storagev2.ParseContext(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImportBytes)

	var req openapi.UsersImportRequest
	if strings.HasPrefix(c.ContentType(), "multipart/") {
		rows, err := readImportCSV(c)
		if err != nil {
			importBadRequest(c, err)
			return
		}
		req.Rows = rows
		req.DeactivateMissing = strings.EqualFold(c.PostForm("deactivate_missing"), "true")
	} else if err := c.ShouldBindJSON(&req); err != nil {
		importBadRequest(c, err)
		return
	}
	if len(req.Rows) > maxImportRows {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"message": fmt.Sprintf("a file import takes at most %d rows", maxImportRows)})
		return
	}

	rows := make([]services.ImportRow, 0, len(req.Rows))
	for i, r := range req.Rows {
		rows = append(rows, services.ImportRow{Row: i + 1, Email: r.Email, Name: r.Name, Groups: r.Groups})
	}
	res, err := services.ImportUsers(models.DB, ctx.OrgID, rows, req.DeactivateMissing)
	switch {
	case errors.Is(err, services.ErrGroupsManaged):
		c.JSON(http.StatusConflict, gin.H{"message": err.Error()})
		return
	case err != nil:
		httputils.AbortWithErr(c, http.StatusInternalServerError, err, "failed importing users")
		return
	}

	out := openapi.UsersImportResponse{
		Created:     res.Created,
		Updated:     res.Updated,
		Deactivated: res.Deactivated,
		Errors:      make([]openapi.UsersImportRowError, 0, len(res.Errors)),
	}
	for _, e := range res.Errors {
		out.Errors = append(out.Errors, openapi.UsersImportRowError{Row: e.Row, Email: e.Email, Message: e.Message})
	}
	c.JSON(http.StatusOK, out)
}

func importBadRequest(c *gin.Context, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"message": "the file is larger than 5 MB"})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
}

// readImportCSV reads the multipart field "file": a header row naming the
// columns (email is required; name and groups are optional, in any order),
// then one user per row. Groups are separated by ";".
func readImportCSV(c *gin.Context) ([]openapi.UsersImportRow, error) {
	fh, err := c.FormFile("file")
	if err != nil {
		return nil, fmt.Errorf("send the CSV as the multipart field \"file\": %w", err)
	}
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseImportCSV(f)
}

func parseImportCSV(r io.Reader) ([]openapi.UsersImportRow, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("failed reading the CSV header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF")))] = i
	}
	emailCol, ok := col["email"]
	if !ok {
		return nil, errors.New("the CSV header must name an email column (email,name,groups)")
	}
	field := func(rec []string, name string) string {
		i, ok := col[name]
		if !ok || i >= len(rec) {
			return ""
		}
		return strings.TrimSpace(rec[i])
	}

	var rows []openapi.UsersImportRow
	for {
		rec, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed reading the CSV: %w", err)
		}
		if len(rows) >= maxImportRows {
			return nil, fmt.Errorf("a file import takes at most %d rows", maxImportRows)
		}
		row := openapi.UsersImportRow{Name: field(rec, "name")}
		if emailCol < len(rec) {
			row.Email = strings.TrimSpace(rec[emailCol])
		}
		for _, g := range strings.Split(field(rec, "groups"), ";") {
			if g = strings.TrimSpace(g); g != "" {
				row.Groups = append(row.Groups, g)
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
