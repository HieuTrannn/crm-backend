//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/HieuTrannn/crm-backend/internal/importer"
)

// uploadCSV posts content as the "file" part of a multipart/form-data body.
func uploadCSV(t *testing.T, ae apiEnv, cookie *http.Cookie, content []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "contacts.csv")
	require.NoError(t, err)
	_, err = fw.Write(content)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/imports/contacts", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	ae.h.ServeHTTP(rec, req)
	return rec
}

func startImport(t *testing.T, ae apiEnv, content []byte) importer.Job {
	t.Helper()
	rec := uploadCSV(t, ae, ae.cookieA, content)
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	var job importer.Job
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &job))
	return job
}

// waitJob polls GET /imports/{id} like the frontend until the job has ended.
func waitJob(t *testing.T, ae apiEnv, id uuid.UUID) importer.Job {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		rec := call(t, ae.h, http.MethodGet, "/api/v1/imports/"+id.String(), nil, ae.cookieA)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var job importer.Job
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &job))
		if job.Status != importer.StatusPending && job.Status != importer.StatusRunning {
			return job
		}
		require.True(t, time.Now().Before(deadline), "job still %s", job.Status)
		time.Sleep(20 * time.Millisecond)
	}
}

type errorPage struct {
	Data       []importer.RowError `json:"data"`
	NextCursor *string             `json:"next_cursor"`
}

func importErrors(t *testing.T, ae apiEnv, id uuid.UUID, q url.Values) errorPage {
	t.Helper()
	rec := call(t, ae.h, http.MethodGet, "/api/v1/imports/"+id.String()+"/errors?"+q.Encode(), nil, ae.cookieA)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var p errorPage
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p))
	return p
}

// TestImportContacts covers FR-IMP-01..04: BOM, header matching, valid and
// rejected lines with their exact line numbers, duplicates, progress and paging.
func TestImportContacts(t *testing.T) {
	ae := newAPIEnv(t)
	createContact(t, ae, ae.cookieA, map[string]any{"full_name": "Existing", "email": "dup@x.vn"})

	csv := string(rune(0xFEFF)) + "Full_Name,Email,Phone,Company,Notes,Extra\n" + // line 1 (UTF-8 BOM)
		"Nguyễn Văn An,an@x.vn,0901,Acme,,ignored\n" + // 2 ok
		",b@x.vn,,,,\n" + // 3 no name
		"Trần Bình,not-an-email,,,,\n" + // 4 bad email
		"Lê Chi,DUP@x.vn,,,,\n" + // 5 email of an existing contact (case-insensitive)
		"Phạm Dũng,dung@x.vn,,,\"two\nlines\",\n" + // 6-7 ok, quoted newline
		"Hoàng Em,an@x.vn,,,,\n" + // 8 same email as line 2
		"Vũ Giang,,0123456789012345678901,,,\n" + // 9 phone too long
		"Đặng Hà,ha@x.vn,,,,\n" // 10 ok

	job := startImport(t, ae, []byte(csv))
	assert.Contains(t, []string{importer.StatusPending, importer.StatusRunning}, job.Status, "202 before processing")

	job = waitJob(t, ae, job.ID)
	assert.Equal(t, importer.StatusCompleted, job.Status)
	assert.Equal(t, int32(8), job.TotalRows, "8 records; line 7 belongs to line 6")
	assert.Equal(t, int32(8), job.ProcessedRows)
	assert.Equal(t, int32(3), job.SuccessRows)
	assert.Equal(t, int32(5), job.ErrorRows)
	assert.NotNil(t, job.FinishedAt)
	assert.Nil(t, job.FailureReason)

	t.Run("row errors carry the file line and reason", func(t *testing.T) {
		got := importErrors(t, ae, job.ID, nil)
		assert.Nil(t, got.NextCursor)
		require.Len(t, got.Data, 5)
		want := map[int32]string{
			3: "full_name is required",
			4: "email is not a valid address",
			5: "email already exists",
			8: "email already exists",
			9: "phone is longer than 20",
		}
		for _, e := range got.Data {
			assert.Contains(t, e.Reason, want[e.RowNumber], "line %d", e.RowNumber)
		}
	})

	t.Run("errors are paged by line number", func(t *testing.T) {
		var lines []int32
		q := url.Values{"limit": {"2"}}
		for {
			p := importErrors(t, ae, job.ID, q)
			for _, e := range p.Data {
				lines = append(lines, e.RowNumber)
			}
			if p.NextCursor == nil {
				break
			}
			q.Set("cursor", *p.NextCursor)
		}
		assert.Equal(t, []int32{3, 4, 5, 8, 9}, lines)
	})

	t.Run("valid lines became contacts owned by the importer", func(t *testing.T) {
		names := map[string]bool{}
		for _, c := range listContacts(t, ae, ae.cookieA, url.Values{"limit": {"100"}}).Data {
			names[c.FullName] = true
			if c.FullName == "Phạm Dũng" {
				assert.Equal(t, "two\nlines", *c.Notes)
			}
			if c.FullName == "Nguyễn Văn An" {
				assert.Equal(t, "Acme", *c.Company)
				assert.Equal(t, ae.s.userA, c.OwnerID)
			}
		}
		for _, n := range []string{"Nguyễn Văn An", "Phạm Dũng", "Đặng Hà", "Existing"} {
			assert.True(t, names[n], n)
		}
		assert.False(t, names["Hoàng Em"], "the later duplicate line was rejected")
		assert.Len(t, names, 4)
	})
}

// TestImportFailuresAndIsolation covers bad uploads, cancel rules and NFR-ISO.
func TestImportFailuresAndIsolation(t *testing.T) {
	ae := newAPIEnv(t)

	t.Run("missing full_name column fails the job with a reason", func(t *testing.T) {
		job := waitJob(t, ae, startImport(t, ae, []byte("email\nan@x.vn\n")).ID)
		assert.Equal(t, importer.StatusFailed, job.Status)
		require.NotNil(t, job.FailureReason)
		assert.Contains(t, *job.FailureReason, "full_name")
	})

	t.Run("no file part is 400", func(t *testing.T) {
		rec := call(t, ae.h, http.MethodPost, "/api/v1/imports/contacts", map[string]any{"x": 1}, ae.cookieA)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("a file over 20 MB is 413", func(t *testing.T) {
		big := append([]byte("full_name\n"), bytes.Repeat([]byte("Nguyen Van An\n"), (importer.MaxFileSize/14)+1000)...)
		rec := uploadCSV(t, ae, ae.cookieA, big)
		require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
		assert.Equal(t, "FILE_TOO_LARGE", errorCode(t, rec))
	})

	finished := waitJob(t, ae, startImport(t, ae, []byte("full_name\nAn\n")).ID)
	require.Equal(t, importer.StatusCompleted, finished.Status)

	t.Run("cancelling a finished job is 409", func(t *testing.T) {
		rec := call(t, ae.h, http.MethodPost, "/api/v1/imports/"+finished.ID.String()+"/cancel", nil, ae.cookieA)
		require.Equal(t, http.StatusConflict, rec.Code)
		assert.Equal(t, "JOB_NOT_RUNNING", errorCode(t, rec))
	})

	t.Run("cancelling a running job stops it", func(t *testing.T) {
		var buf bytes.Buffer
		_, err := importer.GenerateCSV(&buf, 50_000)
		require.NoError(t, err)
		job := startImport(t, ae, buf.Bytes())

		rec := call(t, ae.h, http.MethodPost, "/api/v1/imports/"+job.ID.String()+"/cancel", nil, ae.cookieA)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		job = waitJob(t, ae, job.ID)
		assert.Equal(t, importer.StatusCancelled, job.Status)
		assert.Less(t, job.ProcessedRows, int32(50_000), "stopped before the end")
		require.NotNil(t, job.FailureReason)
		assert.Equal(t, "cancelled by user", *job.FailureReason)
	})

	path := "/api/v1/imports/" + finished.ID.String()
	for name, tc := range map[string]struct {
		method, path string
	}{
		"GET A's job":        {http.MethodGet, path},
		"GET A's job errors": {http.MethodGet, path + "/errors"},
		"cancel A's job":     {http.MethodPost, path + "/cancel"},
	} {
		t.Run("isolation/"+name, func(t *testing.T) {
			rec := call(t, ae.h, tc.method, tc.path, nil, ae.cookieB)
			require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
			assert.Equal(t, "NOT_FOUND", errorCode(t, rec))
		})
	}

}
