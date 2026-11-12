package camoufoxnative

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrAuthenticationRequired 表示隔离账户需要恢复登录
var ErrAuthenticationRequired = errors.New("隔离登录态失效")

// ErrRegionNotSupported 表示 AI Studio 不支持当前出口地区
var ErrRegionNotSupported = errors.New("AI Studio 不支持当前出口地区，为该账户或全局代理换用支持地区的出口")

// ErrAgeRequirement 表示账户未满足 AI Studio 的年龄要求
var ErrAgeRequirement = errors.New("账户未满足 AI Studio 的年龄要求（年满 18 岁），在 Google 账户中核对生日或完成年龄验证")

// ErrWorkspaceDisabled 表示账户所属的 Google Workspace 没有开放 AI Studio
var ErrWorkspaceDisabled = errors.New("账户所属的 Google Workspace 没有开放 AI Studio，联系组织管理员开启")

// ErrServiceRestricted 表示 Google 限制该账户使用 AI Studio
var ErrServiceRestricted = errors.New("Google 已限制该账户使用 AI Studio")

// ErrAccountVerification 表示 Google 要求先验证账户才能继续使用 Gemini API
var ErrAccountVerification = errors.New("Google 要求先验证该账户才能继续使用 Gemini API，在官网完成账户验证")

const (
	// AvailableRegionsURL 是官网在出口地区不受支持时跳转的页面
	AvailableRegionsURL = "https://aistudio.google.com/docs/available-regions"
	// AgeRequirementsURL 是官网在账户未满足年龄要求时跳转的页面
	AgeRequirementsURL = "https://aistudio.google.com/docs/troubleshoot-ai-studio#age-requirements"
	// WorkspaceURL 是官网在 Workspace 没有开放 AI Studio 时跳转的页面
	WorkspaceURL = "https://aistudio.google.com/docs/workspace"
	// AccountVerificationURL 是上游要求验证账户时给出的验证页面
	AccountVerificationURL = "https://aistudio.google.com/verify-account"
)

// AccessDenial 是官网拒绝出口或账户使用 AI Studio 的原因与跳转页
type AccessDenial struct {
	Err error
	URL string
}

func (denial *AccessDenial) Error() string {
	return fmt.Sprintf("%v，官网跳转 %s", denial.Err, denial.URL)
}

func (denial *AccessDenial) Unwrap() error {
	return denial.Err
}

// DenialForMessage 返回官网对 GetLoggingContext 403 消息跳转的原因与页面，以及生成请求 403 消息要求验证账户时的验证页面，其他消息返回 nil
func DenialForMessage(message string) *AccessDenial {
	if strings.HasPrefix(message, "Your account must be verified before it can continue using the Gemini API.") {
		return &AccessDenial{Err: ErrAccountVerification, URL: AccountVerificationURL}
	}
	switch message {
	case "Early Access Apps disabled.", "Core data access for Early Access Apps disabled.", "AI Studio app is disabled.":
		return &AccessDenial{Err: ErrWorkspaceDisabled, URL: WorkspaceURL}
	case "Region not supported.":
		return &AccessDenial{Err: ErrRegionNotSupported, URL: AvailableRegionsURL}
	case "Account not supported.":
		return &AccessDenial{Err: ErrAgeRequirement, URL: AgeRequirementsURL}
	}
	return nil
}

// PageDenial 返回 AI Studio 页面载入跳转到的地址对应的拒绝原因，其他地址返回 nil
func PageDenial(location *url.URL) *AccessDenial {
	var reason error
	switch {
	case location.Hostname() == "accounts.google.com" && location.Path == "/info/servicerestricted":
		reason = ErrServiceRestricted
	case location.Hostname() != "aistudio.google.com":
		return nil
	case strings.HasSuffix(location.Path, "/available-regions"):
		reason = ErrRegionNotSupported
	case strings.HasSuffix(location.Path, "/troubleshoot-ai-studio"):
		reason = ErrAgeRequirement
	case strings.HasSuffix(location.Path, "/docs/workspace"):
		reason = ErrWorkspaceDisabled
	default:
		return nil
	}
	return &AccessDenial{Err: reason, URL: location.String()}
}

// StartupStage 表示 Camoufox runtime 的启动阶段
type StartupStage string

const (
	// StartupPreparingBrowser 表示正在准备浏览器配置
	StartupPreparingBrowser StartupStage = "preparing_browser"
	// StartupLaunchingBrowser 表示正在启动浏览器进程
	StartupLaunchingBrowser StartupStage = "launching_browser"
	// StartupConnectingBiDi 表示正在连接 WebDriver BiDi
	StartupConnectingBiDi StartupStage = "connecting_bidi"
	// StartupLoadingAIStudio 表示正在载入 AI Studio 页面
	StartupLoadingAIStudio StartupStage = "loading_ai_studio"
	// StartupLocatingWAA 表示正在定位 WAA 服务
	StartupLocatingWAA StartupStage = "locating_waa"
	// StartupBootstrappingWAA 表示正在执行 WAA Bootstrap
	StartupBootstrappingWAA StartupStage = "bootstrapping_waa"
)

// Options 定义单个 AI Studio 账户的原生 Camoufox runtime
type Options struct {
	ExecutablePath   string
	StorageStatePath string
	Model            string
	Locale           string
	Timezone         string
	Proxy            string
	ProxyBypass      string
	Headless         bool
	AuthUser         int
	CacheDirectory   string
	ReadyTimeout     time.Duration
	Log              io.Writer
	StartupProgress  func(StartupStage)
	// liveView 表示无头画面实时显示在管理页登录窗口，页面保持 Firefox 默认刷新帧率
	liveView bool
}

func (options Options) reportStartup(stage StartupStage) {
	if options.StartupProgress != nil {
		options.StartupProgress(stage)
	}
}

// State 返回原生 runtime 的当前页面与 bootstrap 结果
type State struct {
	PID         int
	PageURL     string
	UserAgent   string
	SnapshotKey string
	Headers     http.Header
}

type storageState struct {
	Cookies []storageCookie `json:"cookies"`
	Origins []storageOrigin `json:"origins"`
}

type storageCookie struct {
	Name         string  `json:"name"`
	Value        string  `json:"value"`
	Domain       string  `json:"domain"`
	Path         string  `json:"path"`
	Expires      float64 `json:"expires"`
	HTTPOnly     bool    `json:"httpOnly"`
	Secure       bool    `json:"secure"`
	SameSite     string  `json:"sameSite"`
	PartitionKey string  `json:"partitionKey,omitempty"`
}

// cookieKey 标识浏览器中的一个 Cookie
type cookieKey struct {
	name, domain, path, partition string
}

// key 返回 Cookie 的标识
func (cookie storageCookie) key() cookieKey {
	return cookieKey{cookie.Name, cookie.Domain, cookie.Path, cookie.PartitionKey}
}

type storageOrigin struct {
	Origin       string             `json:"origin"`
	LocalStorage []localStorageItem `json:"localStorage"`
}

type localStorageItem struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// FrontendBundle 表示地址是官网前端主程序，即页面以 base-js 载入的 boq-makersuite 脚本 m=_b 模块
func FrontendBundle(address *url.URL) bool {
	return strings.EqualFold(address.Hostname(), "www.gstatic.com") && strings.Contains(address.Path, "/boq-makersuite/_/js/") && strings.HasSuffix(address.Path, "/m=_b")
}

// PageCatalog 是会话页面载入时 GetLoggingContext、GetAiStudioBenefitTier 与 ListModels 的响应正文
type PageCatalog struct {
	Logging []byte
	Tier    []byte
	Models  []byte
	// Bundle 是页面载入的官网前端主程序地址
	Bundle string
}
