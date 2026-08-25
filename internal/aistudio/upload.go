package aistudio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
)

const driveAPIBase = "https://www.googleapis.com/drive/v3/files"
const driveUploadURL = "https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart&fields=id"

// UploadRequest 表示一次 Drive 文件上传
type UploadRequest struct {
	AccountID string
	Name      string
	MIME      string
	Data      []byte
}

// DriveTransport 负责使用账户固定出口访问 Google Drive
type DriveTransport interface {
	UploadDrive(context.Context, string, string, UploadRequest) (FileRef, error)
	DownloadDrive(context.Context, string, string, string) (Media, error)
}

// GenerateAccessToken 获取网页账户授权的短期 bearer token
func (c *Client) GenerateAccessToken(ctx context.Context, accountID string) (string, error) {
	body, err := json.Marshal([]any{"users/me"})
	if err != nil {
		return "", err
	}
	response, err := c.do(ctx, "GenerateAccessToken", accountID, "", body, false)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	return parseAccessToken(response.Body)
}

func parseAccessToken(source io.Reader) (string, error) {
	raw, err := io.ReadAll(newSparseJSONReader(source))
	if err != nil {
		return "", fmt.Errorf("读取 GenerateAccessToken: %w", err)
	}
	root, err := rawArray(raw, "$", raw)
	if err != nil {
		return "", withMethod(err, "GenerateAccessToken")
	}
	if len(root) == 0 || isJSONNull(root[0]) {
		return "", &ProtocolEvidenceError{Method: "GenerateAccessToken", Path: "$[0]", Detail: "缺少 bearer token", Raw: raw}
	}
	token, err := rawString(root[0], "$[0]", raw)
	if err != nil {
		return "", withMethod(err, "GenerateAccessToken")
	}
	if strings.TrimSpace(token) == "" {
		return "", &ProtocolEvidenceError{Method: "GenerateAccessToken", Path: "$[0]", Detail: "bearer token 为空", Raw: raw}
	}
	return token, nil
}

// UploadFile 上传文件并返回可用于 GenerateContent 的 Drive 引用
func (c *Client) UploadFile(ctx context.Context, request UploadRequest) (FileRef, error) {
	if strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.MIME) == "" || len(request.Data) == 0 {
		return FileRef{}, fmt.Errorf("上传文件需要名称、MIME 和数据")
	}
	token, err := c.GenerateAccessToken(ctx, request.AccountID)
	if err != nil {
		return FileRef{}, err
	}
	drive, ok := c.transport.(DriveTransport)
	if !ok {
		return FileRef{}, fmt.Errorf("AI Studio transport 不支持 Drive")
	}
	return drive.UploadDrive(ctx, request.AccountID, token, request)
}

// UploadFile 使用一个独占账户完成上传并保存资源绑定
func (s *PooledService) UploadFile(ctx context.Context, request UploadRequest) (FileRef, error) {
	lease, owned, err := resolveAccountLease(ctx, s.pool, AccountSelection{AccountID: strings.TrimSpace(request.AccountID)})
	if err != nil {
		return FileRef{}, err
	}
	request.AccountID = lease.Account().ID
	file, uploadErr := s.client.UploadFile(ContextWithAccountLease(ctx, lease), request)
	if uploadErr == nil {
		uploadErr = lease.BindResource(file.ID, "drive-file")
	}
	if owned {
		uploadErr = errors.Join(uploadErr, lease.Release())
	}
	return file, uploadErr
}

// DownloadFile 使用资源创建账户下载 Drive 文件
func (s *PooledService) DownloadFile(ctx context.Context, fileID string) (Media, error) {
	lease, owned, err := resolveAccountLease(ctx, s.pool, AccountSelection{ResourceID: strings.TrimSpace(fileID)})
	if err != nil {
		return Media{}, err
	}
	accountID := lease.Account().ID
	token, downloadErr := s.client.GenerateAccessToken(ContextWithAccountLease(ctx, lease), accountID)
	var media Media
	if downloadErr == nil {
		drive, ok := s.client.transport.(DriveTransport)
		if !ok {
			downloadErr = fmt.Errorf("AI Studio transport 不支持 Drive")
		} else {
			media, downloadErr = drive.DownloadDrive(ContextWithAccountLease(ctx, lease), accountID, token, fileID)
		}
	}
	if owned {
		downloadErr = errors.Join(downloadErr, lease.Release())
	}
	if DefinitiveAuthenticationFailure(downloadErr) {
		if stateErr := s.pool.MarkAuthRequired(accountID, downloadErr.Error()); stateErr != nil {
			downloadErr = errors.Join(downloadErr, stateErr)
		}
	}
	return media, downloadErr
}

// UploadDrive 通过当前账户固定出口上传 multipart/related 文件
func (t *MakerSuiteHTTPTransport) UploadDrive(ctx context.Context, accountID string, token string, request UploadRequest) (FileRef, error) {
	lease, owned, err := resolveAccountLease(ctx, t.pool, AccountSelection{AccountID: accountID})
	if err != nil {
		return FileRef{}, err
	}
	release := func(operationErr error) error {
		if !owned {
			return operationErr
		}
		return errors.Join(operationErr, lease.Release())
	}
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	metadataHeader := make(textproto.MIMEHeader)
	metadataHeader.Set("Content-Type", "application/json; charset=UTF-8")
	metadata, err := writer.CreatePart(metadataHeader)
	if err != nil {
		return FileRef{}, release(err)
	}
	if err := json.NewEncoder(metadata).Encode(map[string]string{"mimeType": request.MIME, "name": request.Name}); err != nil {
		return FileRef{}, release(err)
	}
	dataHeader := make(textproto.MIMEHeader)
	dataHeader.Set("Content-Type", request.MIME)
	dataPart, err := writer.CreatePart(dataHeader)
	if err != nil {
		return FileRef{}, release(err)
	}
	if _, err := dataPart.Write(request.Data); err != nil {
		return FileRef{}, release(err)
	}
	if err := writer.Close(); err != nil {
		return FileRef{}, release(err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, driveUploadURL, body)
	if err != nil {
		return FileRef{}, release(err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	httpRequest.Header.Set("Content-Type", "multipart/related; boundary="+writer.Boundary())
	client, err := t.clientForProxy(lease.Account().EffectiveProxy(t.globalProxy))
	if err != nil {
		return FileRef{}, release(err)
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return FileRef{}, release(fmt.Errorf("上传 Drive 文件: %w", err))
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return FileRef{}, release(err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return FileRef{}, release(fmt.Errorf("Drive 上传返回 HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody))))
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return FileRef{}, release(fmt.Errorf("解析 Drive 上传响应: %w", err))
	}
	if strings.TrimSpace(result.ID) == "" {
		return FileRef{}, release(fmt.Errorf("Drive 上传响应缺少文件 ID"))
	}
	return FileRef{ID: result.ID, Name: request.Name, MIME: request.MIME}, release(nil)
}

// DownloadDrive 通过当前账户固定出口下载 Drive 文件
func (t *MakerSuiteHTTPTransport) DownloadDrive(ctx context.Context, accountID string, token string, fileID string) (Media, error) {
	lease, owned, err := resolveAccountLease(ctx, t.pool, AccountSelection{AccountID: accountID})
	if err != nil {
		return Media{}, err
	}
	release := func(operationErr error) error {
		if !owned {
			return operationErr
		}
		return errors.Join(operationErr, lease.Release())
	}
	endpoint := driveAPIBase + "/" + url.PathEscape(fileID) + "?alt=media"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Media{}, release(err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	client, err := t.clientForProxy(lease.Account().EffectiveProxy(t.globalProxy))
	if err != nil {
		return Media{}, release(err)
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return Media{}, release(fmt.Errorf("下载 Drive 文件: %w", err))
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return Media{}, release(err)
	}
	if response.StatusCode != http.StatusOK {
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return Media{}, release(&RPCError{
			Method: "DriveDownload", StatusCode: response.StatusCode, Message: message, Raw: append(json.RawMessage(nil), data...),
		})
	}
	media := Media{Data: data, MIME: response.Header.Get("Content-Type"), Name: driveFilename(response.Header.Get("Content-Disposition"))}
	return media, release(nil)
}

func driveFilename(disposition string) string {
	_, parameters, err := mime.ParseMediaType(disposition)
	if err != nil {
		return ""
	}
	return parameters["filename"]
}

// ResourceIDForContents 返回文件内容绑定的代表资源并校验账户一致性
func (pool *AccountPool) ResourceIDForContents(contents []Content) (string, error) {
	resourceID := ""
	owner := ""
	pool.mu.Lock()
	defer pool.mu.Unlock()
	for _, content := range contents {
		for _, part := range content.Parts {
			if part.File == nil {
				continue
			}
			id := strings.TrimSpace(part.File.ID)
			if id == "" {
				return "", fmt.Errorf("文件引用缺少 ID")
			}
			accountID, exists := pool.resources[id]
			if !exists {
				return "", fmt.Errorf("%w: %s", ErrResourceNotFound, id)
			}
			if owner != "" && owner != accountID {
				return "", fmt.Errorf("文件引用绑定了不同账户")
			}
			if resourceID == "" {
				resourceID = id
				owner = accountID
			}
		}
	}
	return resourceID, nil
}

func encodeFilePart(file *FileRef) ([]any, error) {
	if file == nil || file.ID == "" {
		return nil, fmt.Errorf("文件引用缺少 ID")
	}
	wire := make([]any, 6)
	wire[5] = []any{file.ID}
	return wire, nil
}
