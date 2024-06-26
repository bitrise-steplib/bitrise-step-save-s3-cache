package network

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bitrise-io/go-utils/v2/log"
	"github.com/bitrise-io/go-utils/v2/retryhttp"
)

// DefaultUploader ...
type DefaultUploader struct{}

// UploadParams ...
type UploadParams struct {
	APIBaseURL      string
	Token           string
	ArchivePath     string
	ArchiveChecksum string
	ArchiveSize     int64
	CacheKey        string
}

// Upload a cache archive and associate it with the provided cache key
func (u DefaultUploader) Upload(ctx context.Context, params UploadParams, logger log.Logger) error {
	validatedKey, err := validateKey(params.CacheKey, logger)
	if err != nil {
		return err
	}

	client := newAPIClient(retryhttp.NewClient(logger), params.APIBaseURL, params.Token, logger)

	logger.Debugf("Get upload URL")
	prepareUploadRequest := prepareUploadRequest{
		CacheKey:           validatedKey,
		ArchiveFileName:    filepath.Base(params.ArchivePath),
		ArchiveContentType: "application/zstd",
		ArchiveSizeInBytes: params.ArchiveSize,
	}
	resp, err := client.prepareUpload(prepareUploadRequest)
	if err != nil {
		return fmt.Errorf("failed to get upload URL: %w", err)
	}
	logger.Debugf("Upload ID: %s", resp.ID)

	logger.Debugf("")
	logger.Debugf("Upload archive")
	err = client.uploadArchive(params.ArchivePath, resp.UploadMethod, resp.UploadURL, resp.UploadHeaders)
	if err != nil {
		return fmt.Errorf("failed to upload archive: %w", err)
	}

	logger.Debugf("")
	logger.Debugf("Acknowledge upload")
	response, err := client.acknowledgeUpload(resp.ID)
	if err != nil {
		return fmt.Errorf("failed to finalize upload: %w", err)
	}

	logger.Debugf("Upload acknowledged")
	logResponseMessage(response, logger)

	return nil
}

func validateKey(key string, logger log.Logger) (string, error) {
	if strings.Contains(key, ",") {
		return "", fmt.Errorf("commas are not allowed in key")
	}

	if len(key) > maxKeyLength {
		logger.Warnf("Key is too long, truncating it to the first %d characters", maxKeyLength)
		return key[:maxKeyLength], nil
	}
	return key, nil
}

func logResponseMessage(response acknowledgeResponse, logger log.Logger) {
	if response.Message == "" || response.Severity == "" {
		return
	}

	var loggerFn func(format string, v ...interface{})
	switch response.Severity {
	case "debug":
		loggerFn = logger.Debugf
	case "info":
		loggerFn = logger.Infof
	case "warning":
		loggerFn = logger.Warnf
	case "error":
		loggerFn = logger.Errorf
	default:
		loggerFn = logger.Printf
	}

	loggerFn("\n")
	loggerFn(response.Message)
	loggerFn("\n")
}
