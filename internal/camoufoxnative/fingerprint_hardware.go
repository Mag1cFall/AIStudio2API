package camoufoxnative

import (
	"maps"
	"math"
	"math/rand/v2"
	"strings"
)

// weighted 是带出现权重的取值
type weighted[T any] struct {
	value  T
	weight float64
}

// draw 按权重抽取一个取值
func draw[T any](items []weighted[T]) T {
	total := 0.0
	for _, item := range items {
		total += item.weight
	}
	point := rand.Float64() * total
	for _, item := range items {
		if point < item.weight {
			return item.value
		}
		point -= item.weight
	}
	return items[len(items)-1].value
}

// windowsDisplay 是一块 Windows 主屏的物理分辨率与按 Windows 默认缩放规则可能的缩放比例
type windowsDisplay struct {
	width, height int
	scales        []weighted[float64]
}

// firefoxDisplays 是 Firefox 硬件报告 2026-11-02 一周中 Windows 常见主屏物理分辨率的占比，缩放比例按 Windows 依面板像素密度推荐的默认缩放
var firefoxDisplays = []weighted[windowsDisplay]{
	{windowsDisplay{1920, 1080, []weighted[float64]{{1, 0.5}, {1.25, 0.35}, {1.5, 0.15}}}, 0.5115},
	{windowsDisplay{1366, 768, []weighted[float64]{{1, 1}}}, 0.1332},
	{windowsDisplay{1920, 1200, []weighted[float64]{{1, 0.4}, {1.25, 0.3}, {1.5, 0.3}}}, 0.0507},
	{windowsDisplay{2560, 1440, []weighted[float64]{{1, 0.45}, {1.25, 0.3}, {1.5, 0.25}}}, 0.0438},
	{windowsDisplay{1600, 900, []weighted[float64]{{1, 0.85}, {1.25, 0.15}}}, 0.0402},
	{windowsDisplay{3840, 2160, []weighted[float64]{{1.5, 0.55}, {1.25, 0.1}, {1.75, 0.1}, {2, 0.15}, {2.5, 0.1}}}, 0.0210},
	{windowsDisplay{1440, 900, []weighted[float64]{{1, 1}}}, 0.0199},
	{windowsDisplay{1280, 1024, []weighted[float64]{{1, 1}}}, 0.0182},
	{windowsDisplay{1680, 1050, []weighted[float64]{{1, 1}}}, 0.0116},
	{windowsDisplay{1024, 768, []weighted[float64]{{1, 1}}}, 0.0112},
	{windowsDisplay{3440, 1440, []weighted[float64]{{1, 1}}}, 0.0103},
}

// windowsTaskbars 是 Windows 11 与 Windows 10 任务栏以 DIP 计的高度，权重为 Firefox 硬件报告中两者的占比
var windowsTaskbars = []weighted[int]{{48, 0.5127}, {40, 0.2888}}

// firefoxThreads 是 Firefox 硬件报告中各物理核数的占比与对应常见 CPU 的逻辑处理器数
var firefoxThreads = []weighted[[]weighted[int]]{
	{[]weighted[int]{{4, 0.6}, {2, 0.4}}, 0.2374},
	{[]weighted[int]{{8, 0.55}, {4, 0.45}}, 0.2984},
	{[]weighted[int]{{12, 0.7}, {6, 0.3}}, 0.1505},
	{[]weighted[int]{{16, 0.6}, {8, 0.25}, {12, 0.15}}, 0.1209},
	{[]weighted[int]{{12, 0.6}, {16, 0.3}, {20, 0.1}}, 0.0759},
	{[]weighted[int]{{16, 0.6}, {24, 0.4}}, 0.0380},
	{[]weighted[int]{{20, 1}}, 0.0254},
	{[]weighted[int]{{24, 0.4}, {32, 0.4}, {22, 0.2}}, 0.0202},
	{[]weighted[int]{{24, 0.5}, {32, 0.5}}, 0.0332},
}

// firefoxGPUVendors 是 Firefox 硬件报告中 Windows 可能的显卡厂商占比，Microsoft 为没有显卡驱动时的基本显示驱动
var firefoxGPUVendors = map[string]float64{"Intel": 0.6381, "AMD": 0.1422, "NVIDIA": 0.1308, "Microsoft": 0.0122}

// maximizedShare 是新指纹中 Firefox 窗口为最大化状态的比例
const maximizedShare = 0.7

// applyWindowsDisplay 按抽到的主屏、缩放与任务栏写入 Firefox 158 在 Windows 上报告的屏幕、可用区域、devicePixelRatio、外窗、内窗与窗口位置：可用区域为屏幕减任务栏，最大化窗口位于 -b、外窗比可用区域大 2b，b 为 9 个物理像素的 CSS 像素数；普通窗口在可用区域内随机放置
func applyWindowsDisplay(config map[string]any) {
	display := draw(firefoxDisplays)
	scale := draw(display.scales)
	width := int(math.Round(float64(display.width) / scale))
	height := int(math.Round(float64(display.height) / scale))
	availHeight := height - draw(windowsTaskbars)
	border := int(math.Round(9 / scale))
	config["screen.width"], config["screen.height"] = width, height
	config["screen.availWidth"], config["screen.availHeight"] = width, availHeight
	config["screen.availLeft"], config["screen.availTop"] = 0, 0
	config["window.devicePixelRatio"] = scale
	if rand.Float64() < maximizedShare {
		config["window.screenX"], config["window.screenY"] = -border, -border
		config["window.outerWidth"], config["window.outerHeight"] = width+2*border, availHeight+2*border
		config["window.innerWidth"] = width
		config["window.innerHeight"] = availHeight + 2*border - int(math.Round(85+18/scale))
		return
	}
	outerWidth := int(math.Round(float64(width) * (0.55 + 0.4*rand.Float64())))
	outerHeight := int(math.Round(float64(availHeight) * (0.7 + 0.3*rand.Float64())))
	config["window.screenX"], config["window.screenY"] = rand.IntN(width-outerWidth+1), rand.IntN(availHeight-outerHeight+1)
	config["window.outerWidth"], config["window.outerHeight"] = outerWidth, outerHeight
	config["window.innerWidth"] = outerWidth - 2*border
	config["window.innerHeight"] = outerHeight - int(math.Round(83+11/scale))
}

// firefoxHardwareConcurrency 按 Firefox 硬件报告的物理核数占比抽取 navigator.hardwareConcurrency
func firefoxHardwareConcurrency() int {
	return draw(draw(firefoxThreads))
}

// windowsWebGLByVendor 按 Firefox 硬件报告的显卡厂商占比抽取一组 Firefox 158 模糊化后的 Windows WebGL 配置，同一厂商内按官方启动器的权重
func windowsWebGLByVendor() map[string]any {
	vendor := draw([]weighted[string]{{"Intel", firefoxGPUVendors["Intel"]}, {"AMD", firefoxGPUVendors["AMD"]}, {"NVIDIA", firefoxGPUVendors["NVIDIA"]}, {"Microsoft", firefoxGPUVendors["Microsoft"]}})
	var profiles []weighted[map[string]any]
	for _, profile := range windowsWebGLProfiles {
		renderer, _ := profile.Config["webGl:renderer"].(string)
		if profile.Config["webGl:vendor"] == "Google Inc. ("+vendor+")" && strings.HasSuffix(renderer, ", or similar") {
			profiles = append(profiles, weighted[map[string]any]{profile.Config, profile.Weight})
		}
	}
	return maps.Clone(draw(profiles))
}

// windowsLocaleFonts 是 Windows 以该语言为显示语言时自动安装的补充字体，英文名与本地化名都列出
var windowsLocaleFonts = map[string][]string{
	"zh-TW": {"DFKai-SB", "標楷體", "MingLiU", "細明體", "PMingLiU", "新細明體", "MingLiU_HKSCS", "細明體_HKSCS"},
	"zh-CN": {"DengXian", "等线", "DengXian Light", "等线 Light", "FangSong", "仿宋", "KaiTi", "楷体", "SimHei", "黑体"},
	"ja":    {"Meiryo", "メイリオ", "Meiryo UI", "MS Mincho", "ＭＳ 明朝", "MS PMincho", "ＭＳ Ｐ明朝", "Yu Mincho", "游明朝"},
	"ko":    {"Batang", "바탕", "BatangChe", "바탕체", "Dotum", "돋움", "DotumChe", "돋움체", "Gulim", "굴림", "GulimChe", "굴림체", "Gungsuh", "궁서", "GungsuhChe", "궁서체"},
}

// localeFonts 返回语言对应的 Windows 补充字体，繁体中文地区共用 zh-TW
func localeFonts(locale string) []string {
	language, region, _ := strings.Cut(locale, "-")
	switch strings.ToLower(language) {
	case "zh":
		if region == "TW" || region == "HK" || region == "MO" {
			return windowsLocaleFonts["zh-TW"]
		}
		return windowsLocaleFonts["zh-CN"]
	case "ja", "ko":
		return windowsLocaleFonts[strings.ToLower(language)]
	}
	return nil
}
