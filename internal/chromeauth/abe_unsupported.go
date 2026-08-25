//go:build !windows || !amd64

package chromeauth

import "fmt"

func retrieveV20Key(string) ([]byte, error) {
	return nil, fmt.Errorf("自动读取 Chrome App-Bound 主密钥仅支持 Windows amd64")
}
