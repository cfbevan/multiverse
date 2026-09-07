package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
)

const (
	multipartFileFieldName = "file"
	maxMultipartFieldBytes = int64(1 << 20)
)

type multipartUpload struct {
	Values          map[string]string
	HasFile         bool
	FileName        string
	FileContentType string
	FileSize        int64
	FileSHA256      string
	FileData        []byte
}

func parseMultipartUpload(
	w http.ResponseWriter,
	r *http.Request,
	maxBodyBytes int64,
) (*multipartUpload, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, err
	}

	upload := &multipartUpload{Values: map[string]string{}}
	for {
		done, err := processMultipartPart(reader, upload)
		if done {
			break
		}
		if err != nil {
			return nil, err
		}
	}

	return upload, nil
}

func processMultipartPart(reader *multipart.Reader, upload *multipartUpload) (bool, error) {
	part, err := reader.NextPart()
	if err == io.EOF {
		return true, nil
	}
	if err != nil {
		return false, err
	}

	fieldName := strings.TrimSpace(part.FormName())
	if fieldName == "" {
		_ = part.Close()

		return false, nil
	}

	fileName := strings.TrimSpace(part.FileName())
	if fieldName == multipartFileFieldName && fileName != "" {
		if err := readUploadFilePart(part, upload, fileName); err != nil {
			return false, err
		}

		return false, nil
	}

	if err := readUploadValuePart(part, upload, fieldName); err != nil {
		return false, err
	}

	return false, nil
}

func readUploadFilePart(part *multipart.Part, upload *multipartUpload, fileName string) error {
	data, readErr := io.ReadAll(part)
	if closeErr := part.Close(); closeErr != nil && readErr == nil {
		readErr = closeErr
	}
	if readErr != nil {
		return readErr
	}

	hasher := sha256.New()
	if _, err := hasher.Write(data); err != nil {
		return err
	}

	upload.HasFile = true
	upload.FileName = fileName
	upload.FileContentType = strings.TrimSpace(part.Header.Get("Content-Type"))
	upload.FileData = data
	upload.FileSize = int64(len(data))
	upload.FileSHA256 = hex.EncodeToString(hasher.Sum(nil))

	return nil
}

func readUploadValuePart(part *multipart.Part, upload *multipartUpload, fieldName string) error {
	valueBytes, readErr := io.ReadAll(io.LimitReader(part, maxMultipartFieldBytes))
	if closeErr := part.Close(); closeErr != nil && readErr == nil {
		readErr = closeErr
	}
	if readErr != nil {
		return readErr
	}

	upload.Values[fieldName] = strings.TrimSpace(string(valueBytes))

	return nil
}

func bearerTokenFromRequest(r *http.Request, formToken string) string {
	token := strings.TrimSpace(formToken)
	if token != "" {
		return token
	}

	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}

	return ""
}
