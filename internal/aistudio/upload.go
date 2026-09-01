package aistudio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const driveAPIBase = "https://www.googleapis.com/drive/v3/files"
const driveUploadURL = "https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart&fields=id"
const driveResumableUploadURL = "https://www.googleapis.com/upload/drive/v3/files?uploadType=resumable&fields=id"
const driveUploadChunkSize = 8 << 20
const driveCleanupTimeout = 5 * time.Second

// UploadRequest 表示一次 Drive 文件上传
type UploadRequest struct {
	AccountID      string
	Name           string
	MIME           string
	Purpose        string
	Size           int64
	MaxSize        int64
	Reader         io.Reader
	ResolvePurpose func(context.Context) (string, error)
}

type uploadTooLargeError struct {
	limit int64
}

func (err *uploadTooLargeError) Error() string {
	return fmt.Sprintf("file exceeds %d bytes", err.limit)
}

func (err *uploadTooLargeError) HTTPStatus() int {
	return http.StatusRequestEntityTooLarge
}

func (err *uploadTooLargeError) ErrorCode() string {
	return "file_too_large"
}

type uploadCountingReader struct {
	reader io.Reader
	count  int64
}

func (reader *uploadCountingReader) Read(target []byte) (int, error) {
	count, err := reader.reader.Read(target)
	reader.count += int64(count)
	return count, err
}

type boundedUploadReader struct {
	reader    io.Reader
	remaining int64
	limit     int64
}

func (reader *boundedUploadReader) Read(target []byte) (int, error) {
	if reader.remaining > 0 {
		if int64(len(target)) > reader.remaining {
			target = target[:int(reader.remaining)]
		}
		count, err := reader.reader.Read(target)
		reader.remaining -= int64(count)
		return count, err
	}
	var probe [1]byte
	count, err := reader.reader.Read(probe[:])
	if count > 0 {
		return 0, &uploadTooLargeError{limit: reader.limit}
	}
	return 0, err
}

// FileMetadata 表示已上传文件的持久元数据
type FileMetadata struct {
	ID        string
	Name      string
	MIME      string
	Purpose   string
	Size      int64
	CreatedAt time.Time
}

// MediaStream 表示需要调用方关闭的媒体响应流
type MediaStream struct {
	Body io.ReadCloser
	MIME string
	Name string
	Size int64
}

type releaseReadCloser struct {
	body    io.ReadCloser
	release func() error
	once    sync.Once
	err     error
}

type contextWithoutAccountLease struct {
	context.Context
}

func (ctx contextWithoutAccountLease) Value(key any) any {
	if _, ok := key.(accountLeaseContextKey); ok {
		return nil
	}
	return ctx.Context.Value(key)
}

type temporaryDriveCopy struct {
	id    string
	token string
	bound bool
}

// TemporaryFileCopies 保存一次请求创建的临时 Drive 文件
type TemporaryFileCopies struct {
	client  *Client
	lease   *AccountLease
	copies  []temporaryDriveCopy
	sources map[string]struct{}
	once    sync.Once
	err     error
}

// Count 返回本次请求创建的临时文件数
func (copies *TemporaryFileCopies) Count() int {
	if copies == nil {
		return 0
	}
	return len(copies.copies)
}

// SourceAccountIDs 返回临时文件的来源账户
func (copies *TemporaryFileCopies) SourceAccountIDs() []string {
	if copies == nil {
		return nil
	}
	result := make([]string, 0, len(copies.sources))
	for accountID := range copies.sources {
		result = append(result, accountID)
	}
	sort.Strings(result)
	return result
}

// Cleanup 在目标账户租约释放前删除全部临时 Drive 副本
func (copies *TemporaryFileCopies) Cleanup() error {
	if copies == nil {
		return nil
	}
	copies.once.Do(func() {
		if copies.client == nil || copies.lease == nil || copies.lease.Account() == nil {
			copies.err = fmt.Errorf("临时文件副本未初始化")
			return
		}
		for index := len(copies.copies) - 1; index >= 0; index-- {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), driveCleanupTimeout)
			cleanupCtx = ContextWithAccountLease(cleanupCtx, copies.lease)
			copy := copies.copies[index]
			deleteErr := copies.client.deleteDriveFile(
				cleanupCtx, copies.lease.Account().ID, copy.token, copy.id,
			)
			unbindErr := error(nil)
			if deleteErr == nil && copy.bound {
				unbindErr = copies.lease.pool.unbindResourceContext(cleanupCtx, copy.id)
			}
			cancel()
			copies.err = errors.Join(copies.err, deleteErr, unbindErr)
		}
	})
	return copies.err
}

func (closer *releaseReadCloser) Read(target []byte) (int, error) {
	return closer.body.Read(target)
}

func (closer *releaseReadCloser) Close() error {
	closer.once.Do(func() {
		closer.err = closer.body.Close()
		if closer.release != nil {
			closer.err = errors.Join(closer.err, closer.release())
		}
	})
	return closer.err
}

type fileReferenceNotFoundError struct {
	fileID string
}

func (err *fileReferenceNotFoundError) Error() string {
	return fmt.Sprintf("文件引用不存在: %s", err.fileID)
}

func (err *fileReferenceNotFoundError) Unwrap() error {
	return ErrResourceNotFound
}

func (err *fileReferenceNotFoundError) HTTPStatus() int {
	return http.StatusNotFound
}

func (err *fileReferenceNotFoundError) ErrorCode() string {
	return "file_not_found"
}

// FileService 定义公开文件 API 依赖的能力
type FileService interface {
	UploadFile(context.Context, UploadRequest) (FileRef, error)
	FileMetadata(context.Context, string) (FileMetadata, error)
	DownloadFile(context.Context, string) (MediaStream, error)
	DeleteFile(context.Context, string) error
}

// DriveTransport 负责使用账户固定出口访问 Google Drive
type DriveTransport interface {
	UploadDrive(context.Context, string, string, UploadRequest) (FileRef, error)
	DownloadDrive(context.Context, string, string, string) (MediaStream, error)
	DeleteDrive(context.Context, string, string, string) error
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
	file, _, err := c.uploadFile(ctx, request)
	return file, err
}

func (c *Client) uploadFile(ctx context.Context, request UploadRequest) (FileRef, string, error) {
	if strings.TrimSpace(request.Name) == "" || strings.TrimSpace(request.MIME) == "" || request.Size == 0 || request.Reader == nil {
		return FileRef{}, "", fmt.Errorf("上传文件需要名称、MIME 和数据")
	}
	if request.MaxSize < 0 {
		return FileRef{}, "", fmt.Errorf("上传文件大小上限无效")
	}
	if request.Size > 0 && request.MaxSize > 0 && request.Size > request.MaxSize {
		return FileRef{}, "", &uploadTooLargeError{limit: request.MaxSize}
	}
	token, err := c.GenerateAccessToken(ctx, request.AccountID)
	if err != nil {
		return FileRef{}, "", err
	}
	drive, ok := c.transport.(DriveTransport)
	if !ok {
		return FileRef{}, "", fmt.Errorf("AI Studio transport 不支持 Drive")
	}
	file, err := drive.UploadDrive(ctx, request.AccountID, token, request)
	return file, token, err
}

func (c *Client) deleteDriveFile(ctx context.Context, accountID string, token string, fileID string) error {
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("Drive 文件 ID 为空")
	}
	drive, ok := c.transport.(DriveTransport)
	if !ok {
		return fmt.Errorf("AI Studio transport 不支持 Drive")
	}
	return drive.DeleteDrive(ctx, accountID, token, fileID)
}

// UploadFile 使用一个独占账户完成上传并保存资源绑定
func (s *PooledService) UploadFile(ctx context.Context, request UploadRequest) (FileRef, error) {
	purpose := strings.TrimSpace(request.Purpose)
	if purpose == "" && request.ResolvePurpose == nil {
		return FileRef{}, fmt.Errorf("%w: purpose is required", ErrInvalidArgument)
	}
	accountID := strings.TrimSpace(request.AccountID)
	selection := AccountSelection{AccountID: accountID}
	_, pinned := AccountLeaseFromContext(ctx)
	automatic := accountID == "" && !pinned
	source := request.Reader
	maxAttempts := 1
	var candidateAccountIDs []string
	if automatic {
		candidateAccountIDs = s.pool.fileUploadAccountIDs()
		if len(candidateAccountIDs) == 0 {
			return FileRef{}, ErrNoEligibleAccount
		}
		maxAttempts = len(candidateAccountIDs)
	}
	var file FileRef
	var uploadErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if automatic {
			selection.AccountID = candidateAccountIDs[attempt]
		}
		lease, owned, err := resolveAccountLease(ctx, s.pool, selection)
		if err != nil {
			if uploadErr != nil && errors.Is(err, ErrNoEligibleAccount) {
				return file, uploadErr
			}
			return FileRef{}, err
		}
		request.AccountID = lease.Account().ID
		attemptCtx := ContextWithAccountLease(ctx, lease)
		counter := &uploadCountingReader{reader: source}
		request.Reader = counter
		var token string
		file, token, uploadErr = s.client.uploadFile(attemptCtx, request)
		if uploadErr == nil && purpose == "" {
			purpose, uploadErr = request.ResolvePurpose(attemptCtx)
			purpose = strings.TrimSpace(purpose)
			if uploadErr == nil && purpose == "" {
				uploadErr = fmt.Errorf("%w: purpose is required", ErrInvalidArgument)
			}
		}
		if uploadErr == nil {
			uploadErr = lease.BindFileResource(attemptCtx, file, counter.count, purpose)
		}
		if uploadErr != nil && file.ID != "" {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(attemptCtx), driveCleanupTimeout)
			cleanupErr := s.client.deleteDriveFile(cleanupCtx, request.AccountID, token, file.ID)
			cancel()
			if cleanupErr != nil {
				slog.Warn("Drive 文件回收失败", "account", request.AccountID, "file", file.ID, "error", cleanupErr)
			}
		}
		authFailure := DefinitiveAuthenticationFailure(uploadErr)
		stateErr := error(nil)
		if authFailure {
			stateErr = lease.MarkAuthenticationRequired(uploadErr.Error())
		} else if uploadErr == nil {
			stateErr = errors.Join(
				lease.MarkAuthenticationValid(),
				s.pool.ClearCooldownIfGeneration(
					request.AccountID, "", lease.ModelAccessGeneration(), lease.CheckedAt(),
				),
			)
		}
		uploadErr = errors.Join(uploadErr, stateErr)
		releaseErr := error(nil)
		if owned {
			releaseErr = lease.Release()
			uploadErr = errors.Join(uploadErr, releaseErr)
		}
		if uploadErr == nil || !automatic || !authFailure || counter.count != 0 || stateErr != nil || releaseErr != nil {
			return file, uploadErr
		}
	}
	return file, uploadErr
}

func (p *AccountPool) fileUploadAccountIDs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	available := make([]string, 0, len(p.accounts))
	busy := make([]string, 0, len(p.accounts))
	for offset := 0; offset < len(p.accounts); offset++ {
		account := p.accounts[(p.next+offset)%len(p.accounts)]
		if account == nil || !account.Config.Enabled || account.State != AccountReady {
			continue
		}
		if account.active < p.perAccountConcurrency {
			available = append(available, account.ID)
			continue
		}
		busy = append(busy, account.ID)
	}
	return append(available, busy...)
}

// FileMetadata 返回公开上传文件的持久元数据
func (s *PooledService) FileMetadata(ctx context.Context, fileID string) (FileMetadata, error) {
	if err := ctx.Err(); err != nil {
		return FileMetadata{}, err
	}
	return s.pool.FileMetadata(ctx, fileID)
}

// BindFileResource 保存上传文件的账户绑定与公开元数据
func (l *AccountLease) BindFileResource(ctx context.Context, file FileRef, size int64, purpose string) error {
	if l == nil || l.account == nil || l.pool == nil {
		return fmt.Errorf("账户租约未初始化")
	}
	l.operation.Lock()
	defer l.operation.Unlock()
	if l.released {
		return fmt.Errorf("账户租约已释放")
	}
	l.account.storageMu.Lock()
	defer l.account.storageMu.Unlock()
	return l.pool.bindFileResource(ctx, file, l.account.ID, size, purpose)
}

func (p *AccountPool) bindFileResource(
	ctx context.Context,
	file FileRef,
	accountID string,
	size int64,
	purpose string,
) error {
	file.ID = strings.TrimSpace(file.ID)
	file.Name = strings.TrimSpace(file.Name)
	file.MIME = strings.TrimSpace(file.MIME)
	purpose = strings.TrimSpace(purpose)
	if file.ID == "" || file.Name == "" || file.MIME == "" || size <= 0 || purpose == "" {
		return fmt.Errorf("文件元数据不完整")
	}
	_, err := p.updateRuntimeContext(ctx, accountID, func(_ *Account, runtimeState *accountRuntimeState) (bool, func(*Account), error) {
		if owner, exists := p.resources[file.ID]; exists && owner != accountID {
			return false, nil, fmt.Errorf("资源 %s 已绑定账户 %s", file.ID, owner)
		}
		createdAt := time.Now().UTC()
		if existing, exists := runtimeState.Resources[file.ID]; exists && !existing.CreatedAt.IsZero() {
			createdAt = existing.CreatedAt
		}
		runtimeState.Resources[file.ID] = ResourceBinding{
			Kind: "drive-file", Name: file.Name, MIME: file.MIME, Size: size, Purpose: purpose, CreatedAt: createdAt,
		}
		return true, nil, nil
	})
	return err
}

// FileMetadata 返回 runtime-state 中的公开文件元数据
func (p *AccountPool) FileMetadata(ctx context.Context, fileID string) (FileMetadata, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return FileMetadata{}, fmt.Errorf("%w: 文件 ID 为空", ErrResourceNotFound)
	}
	if err := p.refreshResource(ctx, fileID); err != nil {
		return FileMetadata{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	accountID, exists := p.resources[fileID]
	if !exists {
		return FileMetadata{}, fmt.Errorf("%w: %s", ErrResourceNotFound, fileID)
	}
	account := p.byID[accountID]
	if account == nil {
		return FileMetadata{}, fmt.Errorf("资源账户不存在: %s", accountID)
	}
	binding, exists := account.runtime.Resources[fileID]
	if !exists || binding.Kind != "drive-file" || binding.Name == "" || binding.MIME == "" || binding.Size <= 0 || binding.Purpose == "" {
		return FileMetadata{}, fmt.Errorf("%w: %s", ErrResourceNotFound, fileID)
	}
	return FileMetadata{
		ID: fileID, Name: binding.Name, MIME: binding.MIME, Purpose: binding.Purpose,
		Size: binding.Size, CreatedAt: binding.CreatedAt,
	}, nil
}

// DownloadFile 使用资源创建账户下载 Drive 文件
func (s *PooledService) DownloadFile(ctx context.Context, fileID string) (MediaStream, error) {
	lease, owned, err := resolveAccountLease(ctx, s.pool, AccountSelection{ResourceID: strings.TrimSpace(fileID)})
	if err != nil {
		return MediaStream{}, err
	}
	accountID := lease.Account().ID
	token, downloadErr := s.client.GenerateAccessToken(ContextWithAccountLease(ctx, lease), accountID)
	var media MediaStream
	if downloadErr == nil {
		drive, ok := s.client.transport.(DriveTransport)
		if !ok {
			downloadErr = fmt.Errorf("AI Studio transport 不支持 Drive")
		} else {
			media, downloadErr = drive.DownloadDrive(ContextWithAccountLease(ctx, lease), accountID, token, fileID)
		}
	}
	if DefinitiveAuthenticationFailure(downloadErr) {
		downloadErr = errors.Join(downloadErr, lease.MarkAuthenticationRequired(downloadErr.Error()))
	} else if downloadErr == nil {
		downloadErr = errors.Join(
			lease.MarkAuthenticationValid(),
			s.pool.ClearCooldownIfGeneration(
				accountID, "", lease.ModelAccessGeneration(), lease.CheckedAt(),
			),
		)
	}
	if downloadErr == nil && owned {
		media.Body = &releaseReadCloser{body: media.Body, release: lease.Release}
	} else if owned {
		downloadErr = errors.Join(downloadErr, lease.Release())
	}
	if driveFileNotFound(downloadErr, "DriveDownload") {
		unbindErr := s.pool.unbindResourceContext(ctx, fileID)
		if errors.Is(unbindErr, ErrResourceNotFound) {
			unbindErr = nil
		}
		downloadErr = errors.Join(&fileReferenceNotFoundError{fileID: fileID}, unbindErr)
	}
	return media, downloadErr
}

// DeleteFile 使用资源创建账户删除 Drive 文件和持久绑定
func (s *PooledService) DeleteFile(ctx context.Context, fileID string) error {
	fileID = strings.TrimSpace(fileID)
	if _, err := s.pool.FileMetadata(ctx, fileID); err != nil {
		return err
	}
	lease, owned, err := resolveAccountLease(ctx, s.pool, AccountSelection{ResourceID: fileID})
	if err != nil {
		return err
	}
	accountID := lease.Account().ID
	attemptCtx := ContextWithAccountLease(ctx, lease)
	token, deleteErr := s.client.GenerateAccessToken(attemptCtx, accountID)
	if deleteErr == nil {
		deleteErr = s.client.deleteDriveFile(attemptCtx, accountID, token, fileID)
	}
	stateErr := error(nil)
	if DefinitiveAuthenticationFailure(deleteErr) {
		stateErr = lease.MarkAuthenticationRequired(deleteErr.Error())
	} else if deleteErr == nil {
		stateErr = errors.Join(
			lease.MarkAuthenticationValid(),
			s.pool.ClearCooldownIfGeneration(
				accountID, "", lease.ModelAccessGeneration(), lease.CheckedAt(),
			),
		)
	}
	missing := driveFileNotFound(deleteErr, "DriveDelete")
	unbindErr := error(nil)
	if deleteErr == nil || missing {
		unbindErr = s.pool.unbindResourceContext(ctx, fileID)
		if errors.Is(unbindErr, ErrResourceNotFound) {
			unbindErr = nil
		}
	}
	if missing {
		deleteErr = &fileReferenceNotFoundError{fileID: fileID}
	}
	deleteErr = errors.Join(deleteErr, stateErr, unbindErr)
	if owned {
		deleteErr = errors.Join(deleteErr, lease.Release())
	}
	return deleteErr
}

func driveFileNotFound(err error, method string) bool {
	var rpcError *RPCError
	return errors.As(err, &rpcError) && rpcError.Method == method && rpcError.StatusCode == http.StatusNotFound
}

// CopyFileReferencesToLease 将文件引用复制到目标账户并返回改写内容
func (s *PooledService) CopyFileReferencesToLease(
	ctx context.Context,
	target *AccountLease,
	contents []Content,
) ([]Content, *TemporaryFileCopies, error) {
	if s == nil || s.pool == nil || s.client == nil {
		return nil, nil, fmt.Errorf("文件引用服务未初始化")
	}
	if target == nil || target.Account() == nil || target.pool != s.pool {
		return nil, nil, fmt.Errorf("目标账户租约未初始化")
	}
	rewritten := cloneContentsForFileCopies(contents)
	copies := &TemporaryFileCopies{
		client: s.client, lease: target, sources: make(map[string]struct{}),
	}
	relocated := make(map[string]FileRef)
	targetID := target.Account().ID
	for contentIndex := range rewritten {
		for partIndex := range rewritten[contentIndex].Parts {
			part := &rewritten[contentIndex].Parts[partIndex]
			if part.File == nil {
				continue
			}
			fileID := strings.TrimSpace(part.File.ID)
			if fileID == "" {
				return nil, nil, errors.Join(
					fmt.Errorf("%w: 文件引用缺少 ID", ErrInvalidArgument), copies.Cleanup(),
				)
			}
			owner, metadata, err := s.pool.fileReferenceMetadata(ctx, fileID)
			if err != nil {
				return nil, nil, errors.Join(err, copies.Cleanup())
			}
			if owner == targetID {
				continue
			}
			copies.sources[owner] = struct{}{}
			if relocatedFile, exists := relocated[fileID]; exists {
				part.File = &relocatedFile
				continue
			}
			media, err := s.DownloadFile(contextWithoutAccountLease{Context: ctx}, fileID)
			if err != nil {
				return nil, nil, errors.Join(err, copies.Cleanup())
			}
			targetCtx := ContextWithAccountLease(ctx, target)
			copy, token, uploadErr := s.client.uploadFile(targetCtx, UploadRequest{
				AccountID: targetID,
				Name:      metadata.Name,
				MIME:      metadata.MIME,
				Size:      metadata.Size,
				Reader:    media.Body,
			})
			closeErr := media.Body.Close()
			copy.ID = strings.TrimSpace(copy.ID)
			copy.Name = strings.TrimSpace(copy.Name)
			copy.MIME = strings.TrimSpace(copy.MIME)
			if copy.Name == "" {
				copy.Name = metadata.Name
			}
			if copy.MIME == "" {
				copy.MIME = metadata.MIME
			}
			if uploadErr == nil && closeErr == nil && copy.ID == "" {
				uploadErr = fmt.Errorf("临时 Drive 副本缺少 ID")
			}
			if copy.ID != "" {
				copies.copies = append(copies.copies, temporaryDriveCopy{id: copy.ID, token: token})
			}
			if uploadErr == nil && closeErr == nil {
				uploadErr = target.BindFileResource(targetCtx, copy, metadata.Size, metadata.Purpose)
				if uploadErr == nil {
					copies.copies[len(copies.copies)-1].bound = true
				}
			}
			if uploadErr != nil || closeErr != nil {
				if DefinitiveAuthenticationFailure(uploadErr) {
					uploadErr = errors.Join(uploadErr, target.MarkAuthenticationRequired(uploadErr.Error()))
				}
				return nil, nil, errors.Join(uploadErr, closeErr, copies.Cleanup())
			}
			if stateErr := errors.Join(
				target.MarkAuthenticationValid(),
				s.pool.ClearCooldownIfGeneration(
					targetID, "", target.ModelAccessGeneration(), target.CheckedAt(),
				),
			); stateErr != nil {
				return nil, nil, errors.Join(stateErr, copies.Cleanup())
			}
			relocatedFile := FileRef{ID: copy.ID, Name: part.File.Name, MIME: part.File.MIME}
			if strings.TrimSpace(relocatedFile.Name) == "" {
				relocatedFile.Name = metadata.Name
			}
			if strings.TrimSpace(relocatedFile.MIME) == "" {
				relocatedFile.MIME = metadata.MIME
			}
			relocated[fileID] = relocatedFile
			part.File = &relocatedFile
		}
	}
	return rewritten, copies, nil
}

// UploadInlineImagesToLease 将内联图片上传到目标账户并改写为临时 Drive 引用
func (s *PooledService) UploadInlineImagesToLease(
	ctx context.Context,
	target *AccountLease,
	contents []Content,
	temporary *TemporaryFileCopies,
) ([]Content, *TemporaryFileCopies, error) {
	if s == nil || s.pool == nil || s.client == nil {
		return nil, nil, fmt.Errorf("内联图片上传服务未初始化")
	}
	if target == nil || target.Account() == nil || target.pool != s.pool {
		return nil, nil, fmt.Errorf("目标账户租约未初始化")
	}
	if temporary == nil {
		temporary = &TemporaryFileCopies{
			client: s.client, lease: target, sources: make(map[string]struct{}),
		}
	} else if temporary.client != s.client || temporary.lease != target {
		return nil, nil, errors.Join(fmt.Errorf("临时文件账户不匹配"), temporary.Cleanup())
	}
	rewritten := cloneContentsForFileCopies(contents)
	targetID := target.Account().ID
	targetCtx := ContextWithAccountLease(ctx, target)
	type uploadJob struct {
		contentIndex int
		partIndex    int
		name         string
		blob         *Blob
	}
	type uploadResult struct {
		index int
		file  FileRef
		err   error
	}
	jobs := make([]uploadJob, 0)
	imageIndex := 0
	for contentIndex := range rewritten {
		for partIndex := range rewritten[contentIndex].Parts {
			part := &rewritten[contentIndex].Parts[partIndex]
			if part.InlineData == nil || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(part.InlineData.MIME)), "image/") {
				continue
			}
			imageIndex++
			jobs = append(jobs, uploadJob{
				contentIndex: contentIndex,
				partIndex:    partIndex,
				name:         fmt.Sprintf("inline-image-%d", imageIndex),
				blob:         part.InlineData,
			})
		}
	}
	token, uploadErr := s.client.GenerateAccessToken(targetCtx, targetID)
	drive, ok := s.client.transport.(DriveTransport)
	if uploadErr == nil && !ok {
		uploadErr = fmt.Errorf("AI Studio transport 不支持 Drive")
	}
	if uploadErr != nil {
		if DefinitiveAuthenticationFailure(uploadErr) {
			uploadErr = errors.Join(uploadErr, target.MarkAuthenticationRequired(uploadErr.Error()))
		}
		return nil, nil, errors.Join(uploadErr, temporary.Cleanup())
	}
	uploadCtx, cancelUploads := context.WithCancel(targetCtx)
	resultChannel := make(chan uploadResult, len(jobs))
	for index, job := range jobs {
		go func(index int, job uploadJob) {
			file, err := drive.UploadDrive(uploadCtx, targetID, token, UploadRequest{
				AccountID: targetID,
				Name:      job.name,
				MIME:      job.blob.MIME,
				Size:      int64(len(job.blob.Data)),
				Reader:    bytes.NewReader(job.blob.Data),
			})
			file.ID = strings.TrimSpace(file.ID)
			if err == nil && file.ID == "" {
				err = fmt.Errorf("临时 Drive 图片缺少 ID")
			}
			resultChannel <- uploadResult{index: index, file: file, err: err}
		}(index, job)
	}
	results := make([]uploadResult, len(jobs))
	for range jobs {
		result := <-resultChannel
		results[result.index] = result
		if result.err != nil {
			cancelUploads()
		}
	}
	cancelUploads()
	copyOffset := len(temporary.copies)
	for _, result := range results {
		if result.file.ID != "" {
			temporary.copies = append(temporary.copies, temporaryDriveCopy{id: result.file.ID, token: token})
		}
		uploadErr = errors.Join(uploadErr, result.err)
	}
	if uploadErr != nil {
		if DefinitiveAuthenticationFailure(uploadErr) {
			uploadErr = errors.Join(uploadErr, target.MarkAuthenticationRequired(uploadErr.Error()))
		}
		return nil, nil, errors.Join(uploadErr, temporary.Cleanup())
	}
	for index, job := range jobs {
		file := results[index].file
		if bindErr := target.BindFileResource(targetCtx, file, int64(len(job.blob.Data)), "vision"); bindErr != nil {
			return nil, nil, errors.Join(bindErr, temporary.Cleanup())
		}
		temporary.copies[copyOffset+index].bound = true
		part := &rewritten[job.contentIndex].Parts[job.partIndex]
		part.InlineData = nil
		part.File = &file
	}
	stateErr := errors.Join(
		target.MarkAuthenticationValid(),
		s.pool.ClearCooldownIfGeneration(targetID, "", target.ModelAccessGeneration(), target.CheckedAt()),
	)
	if stateErr != nil {
		return nil, nil, errors.Join(stateErr, temporary.Cleanup())
	}
	return rewritten, temporary, nil
}

func cloneContentsForFileCopies(contents []Content) []Content {
	cloned := make([]Content, len(contents))
	for index, content := range contents {
		cloned[index] = content
		cloned[index].Parts = append([]Part(nil), content.Parts...)
	}
	return cloned
}

func (p *AccountPool) fileReferenceMetadata(ctx context.Context, fileID string) (string, FileMetadata, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" {
		return "", FileMetadata{}, fmt.Errorf("%w: 文件 ID 为空", ErrResourceNotFound)
	}
	if err := p.refreshResource(ctx, fileID); err != nil {
		return "", FileMetadata{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	owner, exists := p.resources[fileID]
	if !exists {
		return "", FileMetadata{}, &fileReferenceNotFoundError{fileID: fileID}
	}
	account := p.byID[owner]
	if account == nil {
		return "", FileMetadata{}, fmt.Errorf("资源账户不存在: %s", owner)
	}
	binding, exists := account.runtime.Resources[fileID]
	if !exists || binding.Kind != "drive-file" ||
		binding.Name == "" || binding.MIME == "" || binding.Size <= 0 || binding.Purpose == "" {
		return "", FileMetadata{}, &fileReferenceNotFoundError{fileID: fileID}
	}
	return owner, FileMetadata{
		ID: fileID, Name: binding.Name, MIME: binding.MIME, Purpose: binding.Purpose,
		Size: binding.Size, CreatedAt: binding.CreatedAt,
	}, nil
}

// UploadDrive 通过当前账户固定出口上传 Drive 文件
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
	client, err := t.clientForProxy(lease.Account().EffectiveProxy(t.globalProxy))
	if err != nil {
		return FileRef{}, release(err)
	}
	if request.Size < 0 {
		file, uploadErr := uploadDriveResumable(ctx, client, token, request)
		return file, release(uploadErr)
	}
	file, uploadErr := uploadDriveMultipart(ctx, client, token, request)
	return file, release(uploadErr)
}

func uploadDriveMultipart(ctx context.Context, client *http.Client, token string, request UploadRequest) (FileRef, error) {
	prefix := new(bytes.Buffer)
	writer := multipart.NewWriter(prefix)
	metadataHeader := make(textproto.MIMEHeader)
	metadataHeader.Set("Content-Type", "application/json; charset=UTF-8")
	metadata, err := writer.CreatePart(metadataHeader)
	if err != nil {
		return FileRef{}, err
	}
	if err := json.NewEncoder(metadata).Encode(map[string]string{"mimeType": request.MIME, "name": request.Name}); err != nil {
		return FileRef{}, err
	}
	dataHeader := make(textproto.MIMEHeader)
	dataHeader.Set("Content-Type", request.MIME)
	if _, err := writer.CreatePart(dataHeader); err != nil {
		return FileRef{}, err
	}
	footer := []byte("\r\n--" + writer.Boundary() + "--\r\n")
	body := io.MultiReader(bytes.NewReader(prefix.Bytes()), request.Reader, bytes.NewReader(footer))
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, driveUploadURL, body)
	if err != nil {
		return FileRef{}, err
	}
	httpRequest.ContentLength = int64(prefix.Len()) + request.Size + int64(len(footer))
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	httpRequest.Header.Set("Content-Type", "multipart/related; boundary="+writer.Boundary())
	response, err := client.Do(httpRequest)
	if err != nil {
		return FileRef{}, fmt.Errorf("上传 Drive 文件: %w", err)
	}
	return parseDriveUploadResponse(response, request)
}

func uploadDriveResumable(ctx context.Context, client *http.Client, token string, request UploadRequest) (FileRef, error) {
	metadata, err := json.Marshal(map[string]string{"mimeType": request.MIME, "name": request.Name})
	if err != nil {
		return FileRef{}, err
	}
	initRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, driveResumableUploadURL, bytes.NewReader(metadata))
	if err != nil {
		return FileRef{}, err
	}
	initRequest.Header.Set("Authorization", "Bearer "+token)
	initRequest.Header.Set("Content-Type", "application/json; charset=UTF-8")
	initRequest.Header.Set("X-Upload-Content-Type", request.MIME)
	initResponse, err := client.Do(initRequest)
	if err != nil {
		return FileRef{}, fmt.Errorf("创建 Drive 上传会话: %w", err)
	}
	initBody, readErr := io.ReadAll(initResponse.Body)
	closeErr := initResponse.Body.Close()
	if readErr != nil || closeErr != nil {
		return FileRef{}, errors.Join(readErr, closeErr)
	}
	if initResponse.StatusCode < 200 || initResponse.StatusCode >= 300 {
		return FileRef{}, driveUploadRPCError("DriveUploadInit", initResponse.StatusCode, initBody)
	}
	sessionURL := strings.TrimSpace(initResponse.Header.Get("Location"))
	if sessionURL == "" {
		return FileRef{}, fmt.Errorf("Drive 上传会话缺少 Location")
	}
	source := request.Reader
	if request.MaxSize > 0 {
		source = &boundedUploadReader{reader: source, remaining: request.MaxSize, limit: request.MaxSize}
	}
	buffer := make([]byte, driveUploadChunkSize+1)
	buffered := 0
	offset := int64(0)
	for {
		count, readErr := io.ReadFull(source, buffer[buffered:])
		buffered += count
		switch {
		case readErr == nil:
			if err := putDriveUploadChunk(ctx, client, token, sessionURL, request.MIME, buffer[:driveUploadChunkSize], offset, -1); err != nil {
				return FileRef{}, err
			}
			offset += driveUploadChunkSize
			buffer[0] = buffer[driveUploadChunkSize]
			buffered = 1
		case errors.Is(readErr, io.EOF), errors.Is(readErr, io.ErrUnexpectedEOF):
			if buffered == 0 {
				return FileRef{}, fmt.Errorf("上传文件不能为空")
			}
			total := offset + int64(buffered)
			response, err := sendDriveUploadChunk(ctx, client, token, sessionURL, request.MIME, buffer[:buffered], offset, total)
			if err != nil {
				return FileRef{}, err
			}
			return parseDriveUploadResponse(response, request)
		default:
			return FileRef{}, readErr
		}
	}
}

func putDriveUploadChunk(
	ctx context.Context,
	client *http.Client,
	token string,
	sessionURL string,
	mimeType string,
	data []byte,
	offset int64,
	total int64,
) error {
	response, err := sendDriveUploadChunk(ctx, client, token, sessionURL, mimeType, data, offset, total)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusPermanentRedirect {
		return driveUploadRPCError("DriveUploadChunk", response.StatusCode, responseBody)
	}
	return nil
}

func sendDriveUploadChunk(
	ctx context.Context,
	client *http.Client,
	token string,
	sessionURL string,
	mimeType string,
	data []byte,
	offset int64,
	total int64,
) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, sessionURL, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.ContentLength = int64(len(data))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", mimeType)
	end := offset + int64(len(data)) - 1
	totalValue := "*"
	if total >= 0 {
		totalValue = fmt.Sprintf("%d", total)
	}
	request.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%s", offset, end, totalValue))
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("上传 Drive 文件块: %w", err)
	}
	return response, nil
}

func parseDriveUploadResponse(response *http.Response, request UploadRequest) (FileRef, error) {
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return FileRef{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return FileRef{}, driveUploadRPCError("DriveUpload", response.StatusCode, responseBody)
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return FileRef{}, fmt.Errorf("解析 Drive 上传响应: %w", err)
	}
	if strings.TrimSpace(result.ID) == "" {
		return FileRef{}, fmt.Errorf("Drive 上传响应缺少文件 ID")
	}
	return FileRef{ID: result.ID, Name: request.Name, MIME: request.MIME}, nil
}

func driveUploadRPCError(method string, status int, body []byte) error {
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(status)
	}
	return &RPCError{Method: method, StatusCode: status, Message: message}
}

// DownloadDrive 通过当前账户固定出口下载 Drive 文件
func (t *MakerSuiteHTTPTransport) DownloadDrive(ctx context.Context, accountID string, token string, fileID string) (MediaStream, error) {
	lease, owned, err := resolveAccountLease(ctx, t.pool, AccountSelection{AccountID: accountID})
	if err != nil {
		return MediaStream{}, err
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
		return MediaStream{}, release(err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	client, err := t.clientForProxy(lease.Account().EffectiveProxy(t.globalProxy))
	if err != nil {
		return MediaStream{}, release(err)
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return MediaStream{}, release(fmt.Errorf("下载 Drive 文件: %w", err))
	}
	if response.StatusCode != http.StatusOK {
		data, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			return MediaStream{}, release(errors.Join(readErr, closeErr))
		}
		message := strings.TrimSpace(string(data))
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return MediaStream{}, release(&RPCError{
			Method: "DriveDownload", StatusCode: response.StatusCode, Message: message,
		})
	}
	body := io.ReadCloser(response.Body)
	if owned {
		body = &releaseReadCloser{body: response.Body, release: lease.Release}
	}
	return MediaStream{
		Body: body, MIME: response.Header.Get("Content-Type"),
		Name: driveFilename(response.Header.Get("Content-Disposition")), Size: response.ContentLength,
	}, nil
}

// DeleteDrive 通过当前账户固定出口删除 Drive 文件
func (t *MakerSuiteHTTPTransport) DeleteDrive(ctx context.Context, accountID string, token string, fileID string) error {
	lease, owned, err := resolveAccountLease(ctx, t.pool, AccountSelection{AccountID: accountID})
	if err != nil {
		return err
	}
	release := func(operationErr error) error {
		if !owned {
			return operationErr
		}
		return errors.Join(operationErr, lease.Release())
	}
	endpoint := driveAPIBase + "/" + url.PathEscape(strings.TrimSpace(fileID))
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return release(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	client, err := t.clientForProxy(lease.Account().EffectiveProxy(t.globalProxy))
	if err != nil {
		return release(err)
	}
	response, err := client.Do(request)
	if err != nil {
		return release(fmt.Errorf("删除 Drive 文件: %w", err))
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return release(err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return release(driveUploadRPCError("DriveDelete", response.StatusCode, responseBody))
	}
	return release(nil)
}

func driveFilename(disposition string) string {
	_, parameters, err := mime.ParseMediaType(disposition)
	if err != nil {
		return ""
	}
	return parameters["filename"]
}

// ResourceIDForContents 返回文件内容绑定的代表资源并校验账户一致性
func (pool *AccountPool) ResourceIDForContents(ctx context.Context, contents []Content) (string, error) {
	for _, content := range contents {
		for _, part := range content.Parts {
			if part.File == nil {
				continue
			}
			id := strings.TrimSpace(part.File.ID)
			if id == "" {
				return "", fmt.Errorf("%w: 文件引用缺少 ID", ErrInvalidArgument)
			}
			if err := pool.refreshResource(ctx, id); err != nil {
				return "", err
			}
		}
	}
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
				return "", fmt.Errorf("%w: 文件引用缺少 ID", ErrInvalidArgument)
			}
			accountID, exists := pool.resources[id]
			if !exists {
				return "", &fileReferenceNotFoundError{fileID: id}
			}
			account := pool.byID[accountID]
			binding, bound := account.runtime.Resources[id]
			if !bound || binding.Kind != "drive-file" && binding.Kind != "video-file" {
				return "", fmt.Errorf("%w: 资源 %s 不能作为文件引用", ErrInvalidArgument, id)
			}
			if owner != "" && owner != accountID {
				return "", fmt.Errorf("%w: 文件引用绑定了不同账户", ErrInvalidArgument)
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
