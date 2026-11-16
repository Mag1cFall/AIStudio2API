package camoufoxnative

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Mag1cFall/AIStudio2API/internal/waa/rhythm"
)

// AccountRhythm 返回账户 Camoufox 指纹中保存的点击与粘贴事件节奏，指纹不存在时按当前版本生成并保存
func AccountRhythm(options Options) (rhythm.Rhythm, error) {
	if _, err := AccountFingerprint(options); err != nil {
		return rhythm.Rhythm{}, err
	}
	return loadAccountRhythm(options.StorageStatePath)
}

// loadAccountRhythm 读取账户 Camoufox 指纹中保存的事件节奏
func loadAccountRhythm(storageStatePath string) (rhythm.Rhythm, error) {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(storageStatePath), "camoufox-fingerprint.json"))
	if err != nil {
		return rhythm.Rhythm{}, fmt.Errorf("读取账户 Camoufox 指纹: %w", err)
	}
	var saved savedFingerprint
	if err := json.Unmarshal(data, &saved); err != nil {
		return rhythm.Rhythm{}, fmt.Errorf("解析账户 Camoufox 指纹: %w", err)
	}
	if saved.Rhythm == nil {
		return rhythm.Rhythm{}, errors.New("账户 Camoufox 指纹缺少事件节奏")
	}
	return *saved.Rhythm, nil
}
