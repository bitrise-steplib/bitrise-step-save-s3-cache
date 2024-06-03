package step

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/bitrise-io/go-steputils/v2/cache/network"
	"github.com/bitrise-io/go-utils/retry"
	"github.com/bitrise-io/go-utils/v2/log"
)

const (
	numUploadRetries = 3
	maxKeyLength     = 512
	maxKeyCount      = 8

	// 50MB
	multipartChunkSize = 50_000_000

	// 100MB
	copySizeLimit = 100_000_000

	// The archive checksum is uploaded with the object as metadata.
	// As we can be pretty sure of the integrity of the uploaded archive,
	// so we avoid having to locally pre-calculate the SHA-256 checksum of each (multi)part.
	// Instead we include the full object checksum as metadata, and compare against it at consecutive uploads.
	checksumKey = "full-object-checksum-sha256"
)

// UploadService .
type UploadService struct {
	Client          *s3.Client
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
}

func (u UploadService) Upload(ctx context.Context, params network.UploadParams, logger log.Logger) error {
	validatedKey, err := validateKey(params.CacheKey, logger)
	if err != nil {
		return fmt.Errorf("validate key: %w", err)
	}

	if u.Bucket == "" {
		return fmt.Errorf("bucket name must not be empty")
	}

	if params.ArchivePath == "" {
		return fmt.Errorf("ArchivePath must not be empty")
	}

	if params.ArchiveSize == 0 {
		return fmt.Errorf("ArchiveSize must not be empty")
	}

	cfg, err := loadAWSCredentials(
		ctx,
		u.Region,
		u.AccessKeyID,
		u.SecretAccessKey,
		logger,
	)
	if err != nil {
		return fmt.Errorf("load AWS credentials: %w", err)
	}

	u.Client = s3.NewFromConfig(*cfg)
	return u.uploadWithS3Client(ctx, validatedKey, params, logger)
}

// If the object for cache key & checksum exists in bucket -> extend expiration
// If the object for cache key exists in bucket -> upload -> overwrites existing object & expiration
// If the object is not yet present in bucket -> upload
func (u UploadService) uploadWithS3Client(
	ctx context.Context,
	cacheKey string,
	params network.UploadParams,
	logger log.Logger,
) error {
	awsCacheKey := fmt.Sprintf("%s.%s", cacheKey, "tzst")
	checksum, err := u.findChecksumWithRetry(ctx, awsCacheKey)
	if err != nil {
		return fmt.Errorf("validate object: %w", err)
	}

	if checksum == params.ArchiveChecksum {
		logger.Debugf("Found archive with the same checksum. Extending expiration time...")
		err := u.copyObjectWithRetry(ctx, awsCacheKey, params, logger)
		if err != nil {
			return fmt.Errorf("copy object: %w", err)
		}
		return nil
	}

	logger.Debugf("Uploading archive...")
	err = u.putObjectWithRetry(ctx, awsCacheKey, params)
	if err != nil {
		return fmt.Errorf("upload artifact: %w", err)
	}

	return nil
}

// findChecksumWithRetry tries to find the archive in bucket.
// If the object is present, it returns the saved SHA-256 checksum from metadata.
// If the object isn't present, it returns an empty string.
func (u UploadService) findChecksumWithRetry(ctx context.Context, cacheKey string) (string, error) {
	var checksum string
	err := retry.Times(numUploadRetries).Wait(5 * time.Second).TryWithAbort(func(attempt uint) (error, bool) {
		response, err := u.Client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(u.Bucket),
			Key:    aws.String(cacheKey),
		})
		if err != nil {
			var apiError smithy.APIError
			if errors.As(err, &apiError) {
				switch apiError.(type) {
				case *types.NotFound:
					// continue with upload
					return nil, true
				default:
					return fmt.Errorf("validating object: %w", err), false
				}
			}
		}

		if response != nil && response.Metadata != nil {
			if sha256, ok := response.Metadata[checksumKey]; ok {
				checksum = sha256
			}
		}

		return nil, true
	})

	return checksum, err
}

// By copying an S3 object into itself with the same Storage Class, the expiration date gets extended.
// copyObjectWithRetry uses this trick to extend archive expiration.
func (u UploadService) copyObjectWithRetry(ctx context.Context, cacheKey string, params network.UploadParams, logger log.Logger) error {
	if params.ArchiveSize < copySizeLimit {
		logger.Debugf("Performing simple copy")
		return retry.Times(numUploadRetries).Wait(5 * time.Second).TryWithAbort(func(attempt uint) (error, bool) {
			resp, err := u.Client.CopyObject(ctx, &s3.CopyObjectInput{
				Bucket:       aws.String(u.Bucket),
				Key:          aws.String(cacheKey),
				StorageClass: types.StorageClassStandard,
				CopySource:   aws.String(fmt.Sprintf("%s/%s", u.Bucket, cacheKey)),
				Metadata: map[string]string{
					checksumKey: params.ArchiveChecksum,
				},
			})
			if err != nil {
				return fmt.Errorf("extend expiration: %w", err), false
			}
			if resp != nil && resp.Expiration != nil {
				logger.Debugf("New expiration date is %s", *resp.Expiration)
			}
			return nil, true
		})
	} else {
		// Object bigger than 5GB cannot be copied by CopyObject, MultipartCopy is the way to go
		logger.Debugf("Performing multipart copy")
		return u.copyObjectMultipart(ctx, cacheKey, params, logger)
	}
}

func (u UploadService) putObjectWithRetry(ctx context.Context, cacheKey string, params network.UploadParams) error {
	return retry.Times(numUploadRetries).Wait(5 * time.Second).TryWithAbort(func(attempt uint) (error, bool) {
		file, err := os.Open(params.ArchivePath)
		if err != nil {
			return fmt.Errorf("open archive path: %w", err), true
		}
		defer file.Close() //nolint:errcheck

		uploader := manager.NewUploader(u.Client, func(u *manager.Uploader) {
			u.PartSize = multipartChunkSize
			u.Concurrency = runtime.NumCPU()
		})

		_, err = uploader.Upload(ctx, &s3.PutObjectInput{
			Body:              file,
			Bucket:            aws.String(u.Bucket),
			Key:               aws.String(cacheKey),
			ContentType:       aws.String("application/zstd"),
			ContentLength:     aws.Int64(params.ArchiveSize),
			ContentEncoding:   aws.String("zstd"),
			ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
			Metadata: map[string]string{
				checksumKey: params.ArchiveChecksum,
			},
		})
		if err != nil {
			return fmt.Errorf("upload artifact: %w", err), false
		}

		return nil, true
	})
}

// perform multipart oject copy concurrently
func (u UploadService) copyObjectMultipart(ctx context.Context, cacheKey string, params network.UploadParams, logger log.Logger) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	initUploadInput := s3.CreateMultipartUploadInput{
		Bucket:            aws.String(u.Bucket),
		Key:               aws.String(cacheKey),
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
		StorageClass:      types.StorageClassStandard,
		Metadata: map[string]string{
			checksumKey: params.ArchiveChecksum,
		},
	}

	var operationID string
	initUploadResponse, err := u.Client.CreateMultipartUpload(ctx, &initUploadInput)
	if err != nil {
		return fmt.Errorf("start multipart copy: %w", err)
	}
	if initUploadResponse != nil && initUploadResponse.UploadId != nil {
		if *initUploadResponse.UploadId == "" {
			return fmt.Errorf("upload ID was empty: %w", err)
		}
		operationID = *initUploadResponse.UploadId
	}

	var wg sync.WaitGroup
	completed := make(chan types.CompletedPart)
	errc := make(chan error)

	completedParts := make([]types.CompletedPart, 0)
	partID := 1
	logger.Debugf("Will copy %d parts", params.ArchiveSize/multipartChunkSize+1)
	for i := 0; i < int(params.ArchiveSize); i += multipartChunkSize {
		wg.Add(1)

		go func(wg *sync.WaitGroup, iteration int, partID int) {
			defer wg.Done()
			sourceRange := u.copyObjectSourceRange(iteration, int(params.ArchiveSize))
			multipartInput := &s3.UploadPartCopyInput{
				Bucket:          aws.String(u.Bucket),
				CopySource:      aws.String(fmt.Sprintf("%s/%s", u.Bucket, cacheKey)),
				CopySourceRange: aws.String(sourceRange),
				Key:             aws.String(cacheKey),
				PartNumber:      aws.Int32(int32(partID)),
				UploadId:        aws.String(operationID),
			}

			multipartUploadResponse, err := u.Client.UploadPartCopy(ctx, multipartInput)
			if err != nil {
				logger.Debugf("Aborting multipart copy")
				u.Client.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{ //nolint:errcheck
					UploadId: aws.String(operationID),
				})
				errc <- fmt.Errorf("abort multipart copy operation: %w", err)
			}
			if multipartUploadResponse != nil && multipartUploadResponse.CopyPartResult != nil && multipartUploadResponse.CopyPartResult.ETag != nil {
				etag := strings.Trim(*multipartUploadResponse.CopyPartResult.ETag, "\"")
				completedParts = append(completedParts, types.CompletedPart{
					ETag:           aws.String(etag),
					PartNumber:     aws.Int32(int32(partID)),
					ChecksumSHA256: multipartUploadResponse.CopyPartResult.ChecksumSHA256,
				})
			}
			logger.Debugf("Multipart copy part #%d completed", partID)
		}(&wg, i, partID)
		partID++
	}

	go func() {
		wg.Wait()
		close(completed)
		close(errc)
	}()

	go func(cancel context.CancelFunc) {
		for eCh := range errc {
			if eCh != nil {
				logger.Errorf("multipart copy: %w", err)
				cancel()
			}
		}
	}(cancel)

	for c := range completed {
		completedParts = append(completedParts, c)
	}

	sort.Slice(completedParts, func(i, j int) bool {
		return *completedParts[i].PartNumber < *completedParts[j].PartNumber
	})

	parts := &types.CompletedMultipartUpload{
		Parts: completedParts,
	}
	completeCopyInput := &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(u.Bucket),
		Key:             aws.String(cacheKey),
		UploadId:        aws.String(operationID),
		MultipartUpload: parts,
	}

	completeCopyOutput, err := u.Client.CompleteMultipartUpload(ctx, completeCopyInput)
	if err != nil {
		return fmt.Errorf("coplete multipart copy: %w", err)
	}
	if completeCopyOutput != nil && completeCopyOutput.Expiration != nil {
		logger.Debugf("New expiration date is %s", *completeCopyOutput.Expiration)
	}
	return nil
}

func (u UploadService) copyObjectSourceRange(part int, archiveSize int) string {
	end := part + multipartChunkSize - 1
	if end > int(archiveSize) {
		end = int(archiveSize) - 1
	}
	return fmt.Sprintf("bytes=%d-%d", part, end)
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

func loadAWSCredentials(
	ctx context.Context,
	region string,
	accessKeyID string,
	secretKey string,
	logger log.Logger,
) (*aws.Config, error) {
	if region == "" {
		return nil, fmt.Errorf("region must not be empty")
	}

	opts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
	}

	if accessKeyID != "" && secretKey != "" {
		logger.Debugf("aws credentials provided, using them...")
		opts = append(opts,
			config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKeyID, secretKey, "")))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load config, %v", err)
	}

	return &cfg, nil
}
