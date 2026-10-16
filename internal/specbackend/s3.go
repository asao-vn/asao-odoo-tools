package specbackend

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// s3Store keeps a backend's objects in an S3 bucket, or in any store that speaks S3's API: Cloudflare
// R2, MinIO, Backblaze B2. Requests are signed with AWS Signature Version 4 by this file alone, so the
// command line carries no SDK.
type s3Store struct {
	bucket    string
	prefix    string
	region    string
	endpoint  *url.URL
	pathStyle bool
	keys      s3Keys
	client    *http.Client
	now       func() time.Time
}

// s3Keys are the credentials a request is signed with. They are read from the environment or the
// shared credentials file when the store is opened, and never written anywhere.
type s3Keys struct {
	accessKey    string
	secretKey    string
	sessionToken string
}

// openS3 reads s3://bucket/prefix?region=...&endpoint=...&path_style=true and the credentials.
func openS3(location *url.URL) (*s3Store, error) {
	bucket := location.Host
	if bucket == "" {
		return nil, fmt.Errorf("%w: s3://<bucket>/<prefix>", ErrBadLocation)
	}
	query := location.Query()
	store := &s3Store{
		bucket: bucket,
		prefix: strings.Trim(location.Path, "/"),
		region: firstOf(query.Get("region"), os.Getenv("AWS_REGION"), os.Getenv("AWS_DEFAULT_REGION"), "us-east-1"),
		client: &http.Client{Timeout: 60 * time.Second},
		now:    time.Now,
	}
	endpoint := firstOf(query.Get("endpoint"), os.Getenv("AWS_ENDPOINT_URL_S3"), os.Getenv("AWS_ENDPOINT_URL"))
	if endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return nil, fmt.Errorf("%w: endpoint %q is not an http(s) URL", ErrBadLocation, endpoint)
		}
		store.endpoint = parsed
		// A store other than AWS is addressed by path unless the location says otherwise.
		store.pathStyle = query.Get("path_style") != "false"
	} else {
		store.endpoint = &url.URL{Scheme: "https", Host: "s3." + store.region + ".amazonaws.com"}
		// A bucket named with dots cannot be a host name under AWS's certificate (*.s3.<region>...):
		// it is addressed by path.
		store.pathStyle = query.Get("path_style") == "true" || strings.Contains(bucket, ".")
	}
	keys, err := s3Credentials()
	if err != nil {
		return nil, err
	}
	store.keys = keys
	return store, nil
}

func firstOf(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// s3Credentials reads AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY (and AWS_SESSION_TOKEN), or the
// profile AWS_PROFILE names (default "default") in the shared credentials file.
func s3Credentials() (s3Keys, error) {
	if access, secret := os.Getenv("AWS_ACCESS_KEY_ID"), os.Getenv("AWS_SECRET_ACCESS_KEY"); access != "" && secret != "" {
		return s3Keys{accessKey: access, secretKey: secret, sessionToken: os.Getenv("AWS_SESSION_TOKEN")}, nil
	}
	path := os.Getenv("AWS_SHARED_CREDENTIALS_FILE")
	if path == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, ".aws", "credentials")
		}
	}
	profile := firstOf(os.Getenv("AWS_PROFILE"), "default")
	if keys, ok := readCredentialsFile(path, profile); ok {
		return keys, nil
	}
	return s3Keys{}, fmt.Errorf("%w: set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, or the profile %q in %s", ErrNoCredentials, profile, path)
}

func readCredentialsFile(path, profile string) (s3Keys, bool) {
	handle, err := os.Open(path)
	if err != nil {
		return s3Keys{}, false
	}
	defer handle.Close()
	var keys s3Keys
	section := ""
	scanner := bufio.NewScanner(handle)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		if section != profile {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(name) {
		case "aws_access_key_id":
			keys.accessKey = strings.TrimSpace(value)
		case "aws_secret_access_key":
			keys.secretKey = strings.TrimSpace(value)
		case "aws_session_token":
			keys.sessionToken = strings.TrimSpace(value)
		}
	}
	return keys, keys.accessKey != "" && keys.secretKey != ""
}

func (s *s3Store) String() string {
	location := "s3://" + s.bucket
	if s.prefix != "" {
		location += "/" + s.prefix
	}
	return location
}

func (s *s3Store) key(name string) string {
	if s.prefix == "" {
		return name
	}
	return s.prefix + "/" + name
}

// objectURL addresses an object (key "" addresses the bucket, for a listing).
func (s *s3Store) objectURL(key string, query url.Values) *url.URL {
	target := *s.endpoint
	path := strings.TrimSuffix(target.Path, "/")
	if s.pathStyle {
		path += "/" + s.bucket
	} else {
		target.Host = s.bucket + "." + target.Host
	}
	if key != "" {
		path += "/" + key
	} else if s.pathStyle {
		path += "/"
	}
	if path == "" {
		path = "/"
	}
	target.Path = path
	target.RawPath = s3EscapePath(path)
	target.RawQuery = s3Query(query)
	return &target
}

// s3EscapePath encodes a path as S3 signs it: every byte but unreserved ones and "/".
func s3EscapePath(path string) string {
	var out strings.Builder
	for index := 0; index < len(path); index++ {
		c := path[index]
		if c == '/' || unreserved(c) {
			out.WriteByte(c)
			continue
		}
		fmt.Fprintf(&out, "%%%02X", c)
	}
	return out.String()
}

func unreserved(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~'
}

func s3Escape(value string) string {
	var out strings.Builder
	for index := 0; index < len(value); index++ {
		c := value[index]
		if unreserved(c) {
			out.WriteByte(c)
			continue
		}
		fmt.Fprintf(&out, "%%%02X", c)
	}
	return out.String()
}

// s3Query is a query string in the order and encoding a signature covers.
func s3Query(query url.Values) string {
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []string
	for _, key := range keys {
		values := append([]string(nil), query[key]...)
		sort.Strings(values)
		for _, value := range values {
			parts = append(parts, s3Escape(key)+"="+s3Escape(value))
		}
	}
	return strings.Join(parts, "&")
}

// sign adds the Signature Version 4 headers to a request whose body is payload.
func (s *s3Store) sign(request *http.Request, payload []byte) {
	moment := s.now().UTC()
	stamp := moment.Format("20060102T150405Z")
	date := moment.Format("20060102")
	payloadHash := sha256Hex(payload)
	request.Header.Set("x-amz-date", stamp)
	request.Header.Set("x-amz-content-sha256", payloadHash)
	if s.keys.sessionToken != "" {
		request.Header.Set("x-amz-security-token", s.keys.sessionToken)
	}
	request.Header.Set("Authorization", signature(request, payloadHash, s.region, s.keys, stamp, date))
}

// signature is the Authorization header of a request: its canonical form, the string to sign and
// the key derived for the day, the region and the service.
func signature(request *http.Request, payloadHash, region string, keys s3Keys, stamp, date string) string {
	host := request.Host
	if host == "" {
		host = request.URL.Host
	}
	headers := map[string]string{"host": host}
	for name, values := range request.Header {
		lower := strings.ToLower(name)
		if lower == "authorization" {
			continue
		}
		if strings.HasPrefix(lower, "x-amz-") || lower == "content-type" || lower == "content-md5" || lower == "range" ||
			lower == "if-none-match" || lower == "if-match" {
			headers[lower] = strings.Join(values, ",")
		}
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + strings.Join(strings.Fields(headers[name]), " ") + "\n")
	}
	signedHeaders := strings.Join(names, ";")
	path := request.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	canonical := strings.Join([]string{request.Method, path, request.URL.RawQuery, canonicalHeaders.String(), signedHeaders, payloadHash}, "\n")
	scope := date + "/" + region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + stamp + "\n" + scope + "\n" + sha256Hex([]byte(canonical))
	key := hmacSHA256([]byte("AWS4"+keys.secretKey), date)
	key = hmacSHA256(key, region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	return fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		keys.accessKey, scope, signedHeaders, hex.EncodeToString(hmacSHA256(key, toSign)))
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// s3Error is what a store answered when it refused a request.
type s3Error struct {
	Status  int
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func (e *s3Error) Error() string {
	return fmt.Sprintf("the store answered %d %s: %s", e.Status, e.Code, e.Message)
}

func (s *s3Store) do(ctx context.Context, method, key string, query url.Values, body []byte, header http.Header) (*http.Response, []byte, error) {
	target := s.objectURL(key, query)
	request, err := http.NewRequestWithContext(ctx, method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	// The URL is sent exactly as it was signed.
	request.URL = target
	request.ContentLength = int64(len(body))
	for name, values := range header {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	s.sign(request, body)
	response, err := s.client.Do(request)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
	}
	if response.StatusCode >= 300 {
		failure := &s3Error{Status: response.StatusCode}
		_ = xml.Unmarshal(data, failure)
		if failure.Code == "" {
			failure.Code = http.StatusText(response.StatusCode)
		}
		return response, data, failure
	}
	return response, data, nil
}

func (s *s3Store) Get(ctx context.Context, name string) ([]byte, error) {
	_, data, err := s.do(ctx, http.MethodGet, s.key(name), nil, nil, nil)
	var failure *s3Error
	if errors.As(err, &failure) && (failure.Status == http.StatusNotFound || failure.Code == "NoSuchKey") {
		return nil, ErrNotFound
	}
	return data, s.classify(err)
}

func (s *s3Store) Put(ctx context.Context, name string, data []byte) error {
	_, _, err := s.do(ctx, http.MethodPut, s.key(name), nil, data, http.Header{"Content-Type": {"application/json"}})
	return s.classify(err)
}

// Create writes an object only when there is none: the store compares and sets in one request.
func (s *s3Store) Create(ctx context.Context, name string, data []byte) error {
	_, _, err := s.do(ctx, http.MethodPut, s.key(name), nil, data,
		http.Header{"Content-Type": {"application/json"}, "If-None-Match": {"*"}})
	var failure *s3Error
	if errors.As(err, &failure) && (failure.Status == http.StatusPreconditionFailed || failure.Status == http.StatusConflict) {
		return ErrExists
	}
	return s.classify(err)
}

func (s *s3Store) Delete(ctx context.Context, name string) error {
	_, _, err := s.do(ctx, http.MethodDelete, s.key(name), nil, nil, nil)
	var failure *s3Error
	if errors.As(err, &failure) && failure.Status == http.StatusNotFound {
		return nil
	}
	return s.classify(err)
}

type listResult struct {
	Contents []struct {
		Key          string    `xml:"Key"`
		LastModified time.Time `xml:"LastModified"`
		Size         int64     `xml:"Size"`
	} `xml:"Contents"`
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
}

func (s *s3Store) List(ctx context.Context, folder string) ([]Object, error) {
	prefix := s.key(strings.TrimSuffix(folder, "/") + "/")
	var out []Object
	token := ""
	for {
		query := url.Values{"list-type": {"2"}, "prefix": {prefix}}
		if token != "" {
			query.Set("continuation-token", token)
		}
		_, data, err := s.do(ctx, http.MethodGet, "", query, nil, nil)
		if err != nil {
			return nil, s.classify(err)
		}
		var page listResult
		if err := xml.Unmarshal(data, &page); err != nil {
			return nil, fmt.Errorf("the store's listing is not S3's: %w", err)
		}
		for _, item := range page.Contents {
			out = append(out, Object{Name: strings.TrimPrefix(item.Key, s.key("")), Modified: item.LastModified, Size: item.Size})
		}
		if !page.IsTruncated || page.NextContinuationToken == "" {
			return out, nil
		}
		token = page.NextContinuationToken
	}
}

func (s *s3Store) classify(err error) error {
	var failure *s3Error
	if errors.As(err, &failure) {
		switch failure.Code {
		case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "ExpiredToken", "InvalidToken":
			return fmt.Errorf("%w: %v", ErrDenied, failure)
		case "NoSuchBucket":
			return fmt.Errorf("%w: bucket %s: %v", ErrNotFound, s.bucket, failure)
		}
		if failure.Status == http.StatusForbidden || failure.Status == http.StatusUnauthorized {
			return fmt.Errorf("%w: %v", ErrDenied, failure)
		}
	}
	return err
}
