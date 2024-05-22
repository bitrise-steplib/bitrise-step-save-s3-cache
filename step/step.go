package step

import (
	"fmt"
	"strings"

	"github.com/bitrise-io/go-steputils/v2/cache"
	"github.com/bitrise-io/go-steputils/v2/stepconf"
	"github.com/bitrise-io/go-utils/v2/command"
	"github.com/bitrise-io/go-utils/v2/env"
	"github.com/bitrise-io/go-utils/v2/log"
	"github.com/bitrise-io/go-utils/v2/pathutil"
)

type Input struct {
	Verbose            bool            `env:"verbose,required"`
	Key                string          `env:"key,required"`
	Paths              string          `env:"paths,required"`
	IsKeyUnique        bool            `env:"is_key_unique"`
	AWSBucket          string          `env:"aws_bucket"`
	AWSRegion          string          `env:"aws_region"`
	AWSAccessKeyID     stepconf.Secret `env:"aws_access_key_id"`
	AWSSecretAccessKey stepconf.Secret `env:"aws_secret_access_key"`
}

type SaveCacheStep struct {
	logger         log.Logger
	inputParser    stepconf.InputParser
	commandFactory command.Factory
	pathChecker    pathutil.PathChecker
	pathProvider   pathutil.PathProvider
	pathModifier   pathutil.PathModifier
	envRepo        env.Repository
}

func New(logger log.Logger, inputParser stepconf.InputParser, commandFactory command.Factory, pathChecker pathutil.PathChecker, pathProvider pathutil.PathProvider, pathModifier pathutil.PathModifier, envRepo env.Repository) SaveCacheStep {
	return SaveCacheStep{
		logger:         logger,
		inputParser:    inputParser,
		commandFactory: commandFactory,
		pathChecker:    pathChecker,
		pathProvider:   pathProvider,
		pathModifier:   pathModifier,
		envRepo:        envRepo,
	}
}

func (step SaveCacheStep) Run() error {
	var input Input
	if err := step.inputParser.Parse(&input); err != nil {
		return fmt.Errorf("failed to parse inputs: %w", err)
	}
	stepconf.Print(input)
	step.logger.Println()

	step.logger.EnableDebugLog(input.Verbose)

	uploader := UploadService{
		Bucket:          input.AWSBucket,
		Region:          input.AWSRegion,
		AccessKeyID:     string(input.AWSAccessKeyID),
		SecretAccessKey: string(input.AWSSecretAccessKey),
	}

	return cache.NewSaver(
		step.envRepo,
		step.logger,
		step.pathProvider,
		step.pathModifier,
		step.pathChecker,
		uploader).Save(cache.SaveCacheInput{
		StepId:      "save-cache",
		Verbose:     input.Verbose,
		Key:         input.Key,
		Paths:       strings.Split(input.Paths, "\n"),
		IsKeyUnique: input.IsKeyUnique,
	})
}
