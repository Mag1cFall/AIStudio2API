//go:build !windows || !amd64

package chromeauth

import "fmt"

// Supported 表示本平台能从 Chrome 导入账户
const Supported = false

func retrieveV20Key(string) ([]byte, error) {
	return nil, fmt.Errorf("自动读取 Chrome App-Bound 主密钥仅支持 Windows amd64")
}
