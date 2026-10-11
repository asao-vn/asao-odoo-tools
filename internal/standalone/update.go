package standalone

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/asao-platform/atoc/packages/cli/internal/clicore"
	"github.com/asao-platform/atoc/packages/cli/internal/release"
	"github.com/asao-platform/atoc/packages/odoo/direct"
)

func updateCommand(config programConfig) *clicore.Command {
	checkFlag := clicore.Flag{
		Name:        "check",
		Kind:        clicore.KindBool,
		Description: "Check for available updates without mutating any file or replacing the executable.",
	}
	channelFlag := clicore.Flag{
		Name:        "channel",
		Kind:        clicore.KindString,
		Default:     "stable",
		Description: "Release channel to check (e.g. stable).",
	}
	manifestURLFlag := clicore.Flag{
		Name:        "manifest-url",
		Kind:        clicore.KindString,
		Description: "URL of the release manifest. Can also be set via ODOO_MANIFEST_URL or AERP_MANIFEST_URL.",
	}
	targetFlag := clicore.Flag{
		Name:        "target",
		Kind:        clicore.KindString,
		Description: "Path to executable to update (defaults to current executable).",
	}
	forceFlag := clicore.Flag{
		Name:        "force",
		Kind:        clicore.KindBool,
		Description: "Force update even if version is already current.",
	}

	return local(&clicore.Command{
		Name:    "update",
		Summary: "Inspect release metadata and safely update the standalone executable.",
		Description: `update inspects the release manifest for updates and applies a cryptographically signed update.

TRUST & SECURITY BOUNDARY:
- Only HTTPS manifest and artifact URLs are permitted (loopback HTTP allowed only for deterministic tests).
- Detached Ed25519 signatures and SHA-256 digests are verified before executing or replacing any file.
- The downloaded binary is probed with "version --output json" in a clean environment without credentials.
- Product identity, catalog identity, semantic contract identity, and execution build identity must match.
- System directories (e.g. /usr/local/bin, /usr/bin) are refused to avoid unmanaged overwrites.
- An atomic rename is performed with a versioned backup kept for rollback.`,
		SideEffect: clicore.EffectWriteLocalConfig,
		Authority:  localAuthority,
		Flags:      []clicore.Flag{checkFlag, channelFlag, manifestURLFlag, targetFlag, forceFlag},
		Examples: []string{
			config.name + " update --check",
			config.name + " update --channel stable",
			config.name + " update --manifest-url https://releases.example.com/manifest.json",
		},
		Run: func(ctx context.Context, inv *clicore.Invocation) (interface{}, error) {
			checkOnly, _ := inv.Bool("check")
			channel := inv.String("channel")
			if channel == "" {
				channel = "stable"
			}
			var cleanTarget, targetDir string
			if !checkOnly {
				// Resolve target executable early to refuse system paths, symlinks, and unwritable files before network operations
				targetPath := inv.String("target")
				if targetPath == "" {
					targetPath = os.Getenv("ODOO_UPDATE_TARGET")
					if targetPath == "" {
						targetPath = os.Getenv("AERP_UPDATE_TARGET")
					}
				}
				if targetPath == "" {
					exe, err := os.Executable()
					if err != nil {
						return nil, clicore.NewError(clicore.CategoryInternal, "TARGET_RESOLUTION_FAILED", fmt.Sprintf("cannot resolve current executable: %v", err))
					}
					targetPath = exe
				}

				cleanTarget = filepath.Clean(targetPath)

				// Refuse system paths
				if isSystemPath(cleanTarget) {
					return nil, clicore.NewError(clicore.CategoryDenied, "SYSTEM_INSTALLATION_PATH_REFUSED",
						fmt.Sprintf("refusing to mutate executable in system directory: %q", cleanTarget)).
						WithRemediation("system-managed or root installation paths cannot be updated in-place; rerun the release installer or install to a user-owned directory such as ~/.local/bin")
				}

				// Target must exist and not be a symlink
				lstat, err := os.Lstat(cleanTarget)
				if err != nil {
					return nil, clicore.NewError(clicore.CategoryNotFound, "TARGET_NOT_FOUND", fmt.Sprintf("target executable %q not found: %v", cleanTarget, err))
				}
				if lstat.Mode()&os.ModeSymlink != 0 {
					return nil, clicore.NewError(clicore.CategoryDenied, "TARGET_IS_SYMLINK",
						fmt.Sprintf("target path %q is a symbolic link; direct canonical target path required", cleanTarget)).
						WithRemediation("specify the resolved target executable path directly rather than a symlink")
				}

				// Target must be a regular file
				targetInfo, err := os.Stat(cleanTarget)
				if err != nil {
					return nil, clicore.NewError(clicore.CategoryNotFound, "TARGET_NOT_FOUND", fmt.Sprintf("target executable %q not found: %v", cleanTarget, err))
				}
				if !targetInfo.Mode().IsRegular() {
					return nil, clicore.NewError(clicore.CategoryDenied, "TARGET_NOT_REGULAR_FILE", fmt.Sprintf("target path %q is not a regular file", cleanTarget))
				}

				targetDir = filepath.Dir(cleanTarget)
				// Resolve symlinks in parent directory to prevent target safety bypass into system paths
				resolvedDir, err := filepath.EvalSymlinks(targetDir)
				if err == nil {
					resolvedTarget := filepath.Join(resolvedDir, filepath.Base(cleanTarget))
					if isSystemPath(resolvedDir) || isSystemPath(resolvedTarget) {
						return nil, clicore.NewError(clicore.CategoryDenied, "SYSTEM_INSTALLATION_PATH_REFUSED",
							fmt.Sprintf("refusing to mutate executable resolving into system directory: %q", resolvedTarget)).
							WithRemediation("system-managed or root installation paths cannot be updated in-place; rerun the release installer or install to a user-owned directory such as ~/.local/bin")
					}
				}

				if err := checkWritable(cleanTarget, targetDir); err != nil {
					return nil, clicore.NewError(clicore.CategoryDenied, "TARGET_NOT_WRITABLE", fmt.Sprintf("target executable %q or its directory is not writable: %v", cleanTarget, err)).
						WithRemediation("ensure the user owns the binary directory or install to a user-writable path")
				}
			}

			manifestURL := inv.String("manifest-url")
			if manifestURL == "" {
				manifestURL = os.Getenv("ODOO_MANIFEST_URL")
				if manifestURL == "" {
					manifestURL = os.Getenv("AERP_MANIFEST_URL")
				}
			}
			manifestURL = strings.TrimSpace(manifestURL)
			if manifestURL == "" {
				return nil, clicore.ConfigError("MANIFEST_URL_REQUIRED", "manifest URL is not configured; specify --manifest-url or set ODOO_MANIFEST_URL")
			}

			// Load and validate manifest
			manifest, err := release.LoadManifest(ctx, manifestURL)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "network request failed") {
					return nil, clicore.NewError(clicore.CategoryUnavailable, "RELEASE_MANIFEST_UNAVAILABLE", fmt.Sprintf("failed to fetch release manifest from %s: %v", release.RedactURL(manifestURL), release.SanitizeURLError(err))).WithRetryable(true)
				}
				if strings.Contains(err.Error(), "insecure non-HTTPS") || strings.Contains(err.Error(), "malformed manifest URL") {
					return nil, clicore.ConfigError("MANIFEST_URL_INVALID", "%s", release.SanitizeURLError(err).Error())
				}
				return nil, clicore.NewError(clicore.CategoryConflict, "RELEASE_MANIFEST_INVALID", fmt.Sprintf("invalid release manifest: %v", release.SanitizeURLError(err)))
			}

			// Validate manifest product matches standalone canonical product identity
			if manifest.Product != release.ProductIdentityStandaloneAERP {
				return nil, clicore.NewError(clicore.CategoryConflict, "RELEASE_MANIFEST_PRODUCT_MISMATCH",
					fmt.Sprintf("manifest product %q does not match standalone product %q", manifest.Product, release.ProductIdentityStandaloneAERP))
			}

			// Select artifact for current platform and requested channel
			artifact, err := manifest.FindRelease(channel, runtime.GOOS, runtime.GOARCH)
			if err != nil {
				if strings.Contains(err.Error(), "channel") {
					return nil, clicore.NewError(clicore.CategoryConflict, "RELEASE_CHANNEL_MISMATCH", err.Error())
				}
				return nil, clicore.NewError(clicore.CategoryNotFound, "RELEASE_NOT_FOUND", fmt.Sprintf("no release available for channel %s on %s/%s: %v", channel, runtime.GOOS, runtime.GOARCH, err))
			}

			currentVersion := Version
			availableVersion := artifact.Version
			updateAvailable := release.IsNewerVersion(currentVersion, availableVersion)
			force, _ := inv.Bool("force")

			compat := direct.CatalogProvenance().CompatibilityIdentity

			// Validate selected artifact compatibility identities against current direct Odoo CLI identity
			// before reporting available update in --check and before mutating any file.
			if artifact.CatalogIdentity != compat.CatalogIdentity {
				return nil, clicore.NewError(clicore.CategoryConflict, "COMPATIBILITY_MISMATCH",
					fmt.Sprintf("release artifact catalogIdentity %q does not match current CLI catalog identity %q",
						artifact.CatalogIdentity, compat.CatalogIdentity))
			}
			if artifact.SemanticContractIdentity != compat.SemanticContractIdentity {
				return nil, clicore.NewError(clicore.CategoryConflict, "COMPATIBILITY_MISMATCH",
					fmt.Sprintf("release artifact semanticContractIdentity %q does not match current CLI semantic contract identity %q",
						artifact.SemanticContractIdentity, compat.SemanticContractIdentity))
			}
			if artifact.ExecutionBuildIdentity != compat.ExecutionBuildIdentity {
				return nil, clicore.NewError(clicore.CategoryConflict, "COMPATIBILITY_MISMATCH",
					fmt.Sprintf("release artifact executionBuildIdentity %q does not match current CLI execution build identity %q",
						artifact.ExecutionBuildIdentity, compat.ExecutionBuildIdentity))
			}

			// If check-only: return structured metadata without touching any file
			if checkOnly {
				return map[string]interface{}{
					"program":                         config.name,
					"product":                         "STANDALONE_AERP",
					"currentVersion":                  currentVersion,
					"availableVersion":                availableVersion,
					"updateAvailable":                 updateAvailable,
					"channel":                         channel,
					"os":                              runtime.GOOS,
					"arch":                            runtime.GOARCH,
					"url":                             release.RedactURL(artifact.URL),
					"sha256":                          artifact.SHA256,
					"catalogIdentity":                 artifact.CatalogIdentity,
					"semanticContractIdentity":        artifact.SemanticContractIdentity,
					"executionBuildIdentity":          artifact.ExecutionBuildIdentity,
					"currentCatalogIdentity":          compat.CatalogIdentity,
					"currentSemanticContractIdentity": compat.SemanticContractIdentity,
					"currentExecutionBuildIdentity":   compat.ExecutionBuildIdentity,
				}, nil
			}

			// Mutating path: if up-to-date and not forced, return cleanly
			if !updateAvailable && !force {
				return map[string]interface{}{
					"program":          config.name,
					"product":          "STANDALONE_AERP",
					"updated":          false,
					"message":          "binary is already up to date",
					"currentVersion":   currentVersion,
					"availableVersion": availableVersion,
					"channel":          channel,
				}, nil
			}

			// Create temporary file beside the target
			tmpFile, err := os.CreateTemp(targetDir, ".odoo-update-*.tmp")
			if err != nil {
				return nil, clicore.NewError(clicore.CategoryInternal, "TEMP_FILE_CREATE_FAILED", fmt.Sprintf("failed to create temporary update file in %q: %v", targetDir, err))
			}
			tmpPath := tmpFile.Name()
			success := false
			defer func() {
				if !success {
					_ = tmpFile.Close()
					_ = os.Remove(tmpPath)
				}
			}()

			// Restrictive file mode
			if err := tmpFile.Chmod(0700); err != nil {
				return nil, clicore.NewError(clicore.CategoryInternal, "TEMP_FILE_CHMOD_FAILED", fmt.Sprintf("failed to set restrictive permissions: %v", err))
			}

			// Download artifact into temporary file
			hasher := sha256.New()
			writer := io.MultiWriter(tmpFile, hasher)
			if err := downloadArtifact(ctx, artifact.URL, writer); err != nil {
				return nil, clicore.NewError(clicore.CategoryUnavailable, "ARTIFACT_DOWNLOAD_FAILED", fmt.Sprintf("failed to download artifact: %v", release.SanitizeURLError(err))).WithRetryable(true)
			}
			if err := tmpFile.Sync(); err != nil {
				return nil, clicore.NewError(clicore.CategoryInternal, "FSYNC_FAILED", fmt.Sprintf("failed to fsync temp file: %v", err))
			}

			// SHA-256 verification
			actualHash := hex.EncodeToString(hasher.Sum(nil))
			if actualHash != strings.ToLower(artifact.SHA256) {
				return nil, clicore.NewError(clicore.CategoryConflict, "ARTIFACT_HASH_MISMATCH",
					fmt.Sprintf("SHA-256 mismatch: calculated %s, expected %s", actualHash, artifact.SHA256))
			}

			// Detached Ed25519 signature verification
			pubKey, err := release.GetPublicKey()
			if err != nil {
				return nil, clicore.ConfigError("PUBLIC_KEY_INVALID", "cannot resolve release public key: %v", err)
			}

			if _, err := tmpFile.Seek(0, 0); err != nil {
				return nil, clicore.NewError(clicore.CategoryInternal, "TEMP_FILE_SEEK_FAILED", err.Error())
			}

			if err := release.VerifyArtifact(tmpFile, *artifact, pubKey); err != nil {
				return nil, clicore.NewError(clicore.CategoryConflict, "ARTIFACT_SIGNATURE_INVALID", fmt.Sprintf("artifact signature verification failed: %v", err))
			}
			if err := tmpFile.Close(); err != nil {
				return nil, clicore.NewError(clicore.CategoryInternal, "TEMP_FILE_CLOSE_FAILED", fmt.Sprintf("failed to close temp file: %v", err))
			}

			// Make executable for probing
			if err := os.Chmod(tmpPath, 0755); err != nil {
				return nil, clicore.NewError(clicore.CategoryInternal, "TEMP_FILE_CHMOD_FAILED", fmt.Sprintf("failed to set executable mode: %v", err))
			}

			// Probe the downloaded binary with `version --output json`
			if err := probeDownloadedBinary(ctx, tmpPath, *artifact); err != nil {
				return nil, clicore.NewError(clicore.CategoryConflict, "COMPATIBILITY_MISMATCH", fmt.Sprintf("downloaded binary failed compatibility check: %v", err))
			}

			// Create versioned backup of the current target
			backupPath := fmt.Sprintf("%s.backup-%s", cleanTarget, currentVersion)
			if err := copyExecutable(cleanTarget, backupPath); err != nil {
				return nil, clicore.NewError(clicore.CategoryInternal, "BACKUP_FAILED", fmt.Sprintf("failed to create versioned backup at %q: %v", backupPath, err))
			}
			if err := fsyncDir(targetDir); err != nil {
				return nil, clicore.NewError(clicore.CategoryInternal, "FSYNC_FAILED", fmt.Sprintf("failed to fsync target directory: %v", err))
			}

			// Atomically rename verified new binary over current target
			if err := os.Rename(tmpPath, cleanTarget); err != nil {
				return nil, clicore.NewError(clicore.CategoryInternal, "REPLACE_FAILED", fmt.Sprintf("atomic replacement failed: %v", err))
			}
			success = true

			// Best-effort directory fsync for metadata durability after rename.
			// Platform note: On Windows or certain virtual/network filesystems (e.g. NFS, SMB, FAT32),
			// fsync on directory descriptors is unsupported. Since the atomic replacement has already
			// completed and mutated the executable on disk, failing the operation here would report an error
			// even though the update actually took effect. Therefore, we treat post-replacement fsync
			// failure as a durability degradation rather than a command failure.
			var durabilityNotice string
			if err := fsyncDir(targetDir); err != nil {
				durabilityNotice = fmt.Sprintf("directory fsync degraded: %v", err)
			}

			result := map[string]interface{}{
				"program":                  config.name,
				"product":                  "STANDALONE_AERP",
				"updated":                  true,
				"previousVersion":          currentVersion,
				"currentVersion":           artifact.Version,
				"targetPath":               cleanTarget,
				"backupPath":               backupPath,
				"channel":                  channel,
				"os":                       artifact.OS,
				"arch":                     artifact.Arch,
				"url":                      release.RedactURL(artifact.URL),
				"catalogIdentity":          artifact.CatalogIdentity,
				"semanticContractIdentity": artifact.SemanticContractIdentity,
				"executionBuildIdentity":   artifact.ExecutionBuildIdentity,
			}
			if durabilityNotice != "" {
				result["durabilityWarning"] = durabilityNotice
			}
			return result, nil
		},
	})
}

func isSystemPath(p string) bool {
	p = filepath.Clean(p)
	systemPrefixes := []string{
		"/usr/local/bin",
		"/usr/local/sbin",
		"/usr/bin",
		"/usr/sbin",
		"/bin",
		"/sbin",
		"/opt",
		"/etc",
	}
	for _, prefix := range systemPrefixes {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	return false
}

func checkWritable(target, dir string) error {
	// Probe write permission on directory
	probe, err := os.CreateTemp(dir, ".probe-write-*")
	if err != nil {
		return err
	}
	probeName := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probeName)

	// Probe write permission on target file itself
	f, err := os.OpenFile(target, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_ = f.Close()
	return nil
}

func downloadArtifact(ctx context.Context, artifactURL string, dst io.Writer) error {
	u, err := url.Parse(artifactURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("malformed artifact URL: %s", release.RedactURL(artifactURL))
	}
	if u.User != nil {
		return errors.New("artifact URL must not contain user credentials")
	}
	if u.Scheme != "https" {
		if u.Scheme != "http" || !release.IsLoopbackHost(u.Hostname()) {
			return fmt.Errorf("insecure non-HTTPS artifact URL: %s", release.RedactURL(artifactURL))
		}
	}

	dlCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, artifactURL, nil)
	if err != nil {
		return release.SanitizeURLError(err)
	}
	req.Header.Set("User-Agent", "odoo-cli-updater")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return release.SanitizeURLError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("artifact download HTTP request failed with status %d", resp.StatusCode)
	}

	if resp.ContentLength > release.MaxArtifactSizeBytes {
		return fmt.Errorf("artifact Content-Length %d exceeds maximum permitted size of %d bytes", resp.ContentLength, release.MaxArtifactSizeBytes)
	}

	n, err := io.Copy(dst, io.LimitReader(resp.Body, release.MaxArtifactSizeBytes+1))
	if err != nil {
		return release.SanitizeURLError(err)
	}
	if n > release.MaxArtifactSizeBytes {
		return fmt.Errorf("artifact payload exceeds maximum permitted size of %d bytes", release.MaxArtifactSizeBytes)
	}
	return nil
}

func probeDownloadedBinary(ctx context.Context, binaryPath string, expected release.Artifact) error {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, binaryPath, "version", "--output", "json")
	// Clean environment: no credential material or profiles
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}

	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to execute downloaded binary: %w", err)
	}

	var envelope struct {
		Status string                 `json:"status"`
		Data   map[string]interface{} `json:"data"`
		Error  map[string]interface{} `json:"error"`
	}
	if err := json.Unmarshal(out, &envelope); err != nil {
		return fmt.Errorf("downloaded binary version output did not return valid JSON envelope: %w", err)
	}
	if envelope.Status != "OK" {
		return fmt.Errorf("downloaded binary version returned non-OK status: %q", envelope.Status)
	}
	if envelope.Error != nil {
		return fmt.Errorf("downloaded binary version returned error: %v", envelope.Error)
	}

	data := envelope.Data
	if data == nil {
		return errors.New("downloaded binary version output missing data object")
	}

	prod, _ := data["product"].(string)
	if prod != release.ProductIdentityStandaloneAERP {
		return fmt.Errorf("product mismatch: got %q, expected %q", prod, release.ProductIdentityStandaloneAERP)
	}

	cliVer, _ := data["cliVersion"].(string)
	if cliVer != expected.Version {
		return fmt.Errorf("cliVersion mismatch: binary reported %q, expected %q", cliVer, expected.Version)
	}

	if pubOS, ok := data["os"].(string); ok && pubOS != "" && pubOS != expected.OS {
		return fmt.Errorf("os mismatch: binary reported %q, expected %q", pubOS, expected.OS)
	}
	if pubArch, ok := data["arch"].(string); ok && pubArch != "" && pubArch != expected.Arch {
		return fmt.Errorf("arch mismatch: binary reported %q, expected %q", pubArch, expected.Arch)
	}

	compat := direct.CatalogProvenance().CompatibilityIdentity

	catID, _ := data["catalogIdentity"].(string)
	if catID != expected.CatalogIdentity || catID != compat.CatalogIdentity {
		return fmt.Errorf("catalogIdentity mismatch: got %q, expected %q (current CLI: %q)", catID, expected.CatalogIdentity, compat.CatalogIdentity)
	}

	semID, _ := data["semanticContractIdentity"].(string)
	if semID != expected.SemanticContractIdentity || semID != compat.SemanticContractIdentity {
		return fmt.Errorf("semanticContractIdentity mismatch: got %q, expected %q (current CLI: %q)", semID, expected.SemanticContractIdentity, compat.SemanticContractIdentity)
	}

	buildID, _ := data["executionBuildIdentity"].(string)
	if buildID != expected.ExecutionBuildIdentity || buildID != compat.ExecutionBuildIdentity {
		return fmt.Errorf("executionBuildIdentity mismatch: got %q, expected %q (current CLI: %q)", buildID, expected.ExecutionBuildIdentity, compat.ExecutionBuildIdentity)
	}

	return nil
}

func copyExecutable(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}

	success := false
	defer func() {
		_ = out.Close()
		if !success {
			_ = os.Remove(dst)
		}
	}()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return err
	}
	success = true
	return nil
}

// fsyncDir flushes directory changes to durable storage on POSIX systems.
// Platform limitations: On Windows, non-POSIX systems, and certain virtual/network filesystems
// (e.g. NFS, SMB, FAT32), opening or syncing a directory descriptor is unsupported and may return
// EINVAL, EPERM, or EACCES. Because any directory sync errors after rename happen after the atomic
// rename has already committed to disk, fsyncDir handles unsupported platform errors gracefully.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		if errors.Is(err, os.ErrInvalid) || os.IsPermission(err) {
			return nil
		}
		return err
	}
	defer d.Close()

	if err := d.Sync(); err != nil {
		if errors.Is(err, os.ErrInvalid) || strings.Contains(err.Error(), "invalid argument") || strings.Contains(err.Error(), "not supported") {
			return nil
		}
		return err
	}
	return nil
}
