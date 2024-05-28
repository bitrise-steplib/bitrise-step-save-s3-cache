# Save S3 Cache

[![Step changelog](https://shields.io/github/v/release/bitrise-steplib/bitrise-step-save-s3-cache?include_prereleases&label=changelog&color=blueviolet)](https://github.com/bitrise-steplib/bitrise-step-save-s3-cache/releases)

Saves build cache using a cache key. This Step needs to be used in combination with **Restore S3 Cache**.

<details>
<summary>Description</summary>

Saves build cache to an arbitrary S3 bucket using a cache key. This Step needs to be used in combination with **Restore S3 Cache**.

#### About key-based caching

Key-based caching is a concept where cache archives are saved and restored using a unique cache key. One Bitrise project can have multiple cache archives stored simultaneously, and the **Restore S3 Cache Step** downloads a cache archive associated with the key provided as a Step input. The **Save S3 Cache** Step is responsible for uploading the cache archive with an exact key.

Caches can become outdated across builds when something changes in the project (for example, a dependency gets upgraded to a new version). In this case, a new (unique) cache key is needed to save the new cache contents. This is possible if the cache key is dynamic and changes based on the project state (for example, a checksum of the dependency lockfile is part of the cache key). If you use the same dynamic cache key when restoring the cache, the Step will download the most relevant cache archive available.

Key-based caching is platform-agnostic and can be used to cache anything by carefully selecting the cache key and the files/folders to include in the cache.

#### Templates

The Step requires a string key to use when uploading a cache archive. In order to always download the most relevant cache archive for each build, the cache key input can contain template elements. The **Restore S3 cache Step** evaluates the key template at runtime and the final key value can change based on the build environment or files in the repo. Similarly, the **Save S3 cache** Step also uses templates to compute a unique cache key when uploading a cache archive.

The following variables are supported in the **Cache key** input:

- `cache-key-{{ .Branch }}`: Current git branch the build runs on
- `cache-key-{{ .CommitHash }}`: SHA-256 hash of the git commit the build runs on
- `cache-key-{{ .Workflow }}`: Current Bitrise workflow name (eg. `primary`)
- `{{ .Arch }}-cache-key`: Current CPU architecture (`amd64` or `arm64`)
- `{{ .OS }}-cache-key`: Current operating system (`linux` or `darwin`)

Functions available in a template:

`checksum`: This function takes one or more file paths and computes the SHA256 [checksum](https://en.wikipedia.org/wiki/Checksum) of the file contents. This is useful for creating unique cache keys based on files that describe content to cache.

Examples of using `checksum`:
- `cache-key-{{ checksum "package-lock.json" }}`
- `cache-key-{{ checksum "**/Package.resolved" }}`
- `cache-key-{{ checksum "**/*.gradle*" "gradle.properties" }}`

`getenv`: This function returns the value of an environment variable or an empty string if the variable is not defined.

Examples of `getenv`:
- `cache-key-{{ getenv "PR" }}`
- `cache-key-{{ getenv "BITRISEIO_PIPELINE_ID" }}`

#### Key matching

The most straightforward use case is when both the **Save S3 cache** and **Restore S3 cache** Steps use the same exact key to transfer cache between builds. Stored cache archives are scoped to the Bitrise project. Builds can restore caches saved by any previous Workflow run on any Bitrise Stack.

Unlike this Step, the **Restore S3 cache** Step can define multiple keys as fallbacks when there is no match for the first cache key. See the docs of the **Restore S3 cache** Step for more details.

#### Skip saving the cache

The Step can decide to skip saving a new cache entry to avoid unnecessary work. This happens when there is a previously restored cache in the same workflow and the new cache would have the same contents as the one restored.

#### Related steps

[Restore cache](https://github.com/bitrise-steplib/bitrise-step-restore-cache/)

</details>

## 🧩 Get started

Add this step directly to your workflow in the [Bitrise Workflow Editor](https://devcenter.bitrise.io/steps-and-workflows/steps-and-workflows-index/).

You can also run this step directly with [Bitrise CLI](https://github.com/bitrise-io/bitrise).

### Examples

Check out [Workflow Recipes](https://github.com/bitrise-io/workflow-recipes#-key-based-caching-beta) for platform-specific examples!

#### Skip saving the cache in PR builds (only restore)

```yaml
steps:
- restore-cache@1:
    inputs:
    - key: node-modules-{{ checksum "package-lock.json" }}

# Build steps

- save-cache@1:
    run_if: ".IsCI | and (not .IsPR)" # Condition that is false in PR builds
    inputs:
    - key: node-modules-{{ checksum "package-lock.json" }}
    - paths: node_modules
```

#### Separate caches for each OS and architecture

Cache is not guaranteed to work across different Bitrise Stacks (different OS or same OS but different CPU architecture). If a Workflow runs on different stacks, it's a good idea to include the OS and architecture in the **Cache key** input:

```yaml
steps:
- save-cache@1:
    inputs:
    - key: '{{ .OS }}-{{ .Arch }}-npm-cache-{{ checksum "package-lock.json" }}'
    - path: node_modules
```

#### Multiple independent caches

You can add multiple instances of this Step to a Workflow:

```yaml
steps:
- save-cache@1:
    title: Save NPM cache
    inputs:
    - paths: node_modules
    - key: node-modules-{{ checksum "package-lock.json" }}
- save-cache@1:
    title: Save Python cache
    inputs:
    - paths: venv/
    - key: pip-packages-{{ checksum "requirements.txt" }}
```


## ⚙️ Configuration

<details>
<summary>Inputs</summary>

| Key | Description | Flags | Default |
| --- | --- | --- | --- |
| `key` | Key used for saving a cache archive.  The key supports template elements for creating dynamic cache keys. These dynamic keys change the final key value based on the build environment or files in the repo in order to create new cache archives. See the Step description for more details and examples.  The maximum length of a key is 512 characters (longer keys get truncated). Commas (`,`) are not allowed in keys. | required |  |
| `paths` | List of files and folders to include in the cache.  Add one path per line. Each path can contain wildcards (`*` and `**`) that are evaluated at runtime. | required |  |
| `verbose` | Enable logging additional information for troubleshooting | required | `false` |
| `aws_bucket` | Bring your own bucket: exercise full control over the cache location.  The provided AWS bucket acts as cache backend for the Restore Cache step.  The step expects either: - CACHE_AWS_ACCESS_KEY_ID, CACHE_AWS_SECRET_ACCESS_KEY secrets to be setup for the workflow - The build is running on an EC2 instance. In this case, the steps expects the instance to have access to the bucket. | required |  |
| `aws_region` | AWS Region specifies the region where the bucket belongs. | required | `us-east-1` |
| `aws_access_key_id` | The access key id that matches the secret access key.  The credentials need to be from a user that has at least the following permissions in the bucket specified bellow `s3:ListObjects`, `s3:PutObject`, `s3:GetObjectAttributes` and `s3:GetObject`.  If the build instance has S3 access via IAM Instance role, this variable can be left empty.  | sensitive | `$CACHE_AWS_ACCESS_KEY_ID` |
| `aws_secret_access_key` | The secret access key that matches the secret key ID.  The credentials need to be from a user that has at least the following permissions in the bucket specified bellow `s3:ListObjects`, `s3:PutObject`, `s3:GetObjectAttributes` and `s3:GetObject`.  If the build instance has S3 access via IAM Instance role, this variable can be left empty.  | sensitive | `$CACHE_AWS_SECRET_ACCESS_KEY` |
</details>

<details>
<summary>Outputs</summary>
There are no outputs defined in this step
</details>

## 🙋 Contributing

We welcome [pull requests](https://github.com/bitrise-steplib/bitrise-step-save-s3-cache/pulls) and [issues](https://github.com/bitrise-steplib/bitrise-step-save-s3-cache/issues) against this repository.

For pull requests, work on your changes in a forked repository and use the Bitrise CLI to [run step tests locally](https://devcenter.bitrise.io/bitrise-cli/run-your-first-build/).

**Note:** this step's end-to-end tests (defined in `e2e/bitrise.yml`) are working with secrets which are intentionally not stored in this repo. External contributors won't be able to run those tests. Don't worry, if you open a PR with your contribution, we will help with running tests and make sure that they pass.


Learn more about developing steps:

- [Create your own step](https://devcenter.bitrise.io/contributors/create-your-own-step/)
- [Testing your Step](https://devcenter.bitrise.io/contributors/testing-and-versioning-your-steps/)
