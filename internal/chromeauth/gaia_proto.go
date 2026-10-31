package chromeauth

import "fmt"

type gaiaProtoAccount struct {
	Email       string
	DisplayName string
	GaiaID      string
	AuthUser    int
}

func parseVarint(data []byte, offset int) (uint64, int, error) {
	var res uint64
	var shift uint
	for i := offset; i < len(data); i++ {
		b := data[i]
		res |= uint64(b&0x7f) << shift
		shift += 7
		if b&0x80 == 0 {
			return res, i + 1, nil
		}
		if shift >= 64 {
			return 0, 0, fmt.Errorf("varint overflow")
		}
	}
	return 0, 0, fmt.Errorf("unexpected EOF in varint")
}

func parseGaiaAccounts(data []byte) []gaiaProtoAccount {
	var accounts []gaiaProtoAccount
	offset := 0
	defaultIdx := 0

	for offset < len(data) {
		key, nextOffset, err := parseVarint(data, offset)
		if err != nil {
			break
		}
		wireType := key & 0x07
		if wireType != 2 {
			break
		}
		length, nextOffset, err := parseVarint(data, nextOffset)
		if err != nil {
			break
		}
		end := nextOffset + int(length)
		if end > len(data) {
			break
		}
		subData := data[nextOffset:end]
		offset = end

		subOffset := 0
		var acc gaiaProtoAccount
		acc.AuthUser = defaultIdx
		for subOffset < len(subData) {
			subKey, nextSub, err := parseVarint(subData, subOffset)
			if err != nil {
				break
			}
			fieldNum := subKey >> 3
			subWire := subKey & 0x07
			switch subWire {
			case 0: // varint
				val, nextSub2, err := parseVarint(subData, nextSub)
				if err != nil {
					break
				}
				subOffset = nextSub2
				if fieldNum == 7 {
					acc.AuthUser = int(val)
				}
			case 2: // length-delimited string
				l, nextSub2, err := parseVarint(subData, nextSub)
				if err != nil {
					break
				}
				vEnd := nextSub2 + int(l)
				if vEnd > len(subData) {
					break
				}
				sVal := string(subData[nextSub2:vEnd])
				subOffset = vEnd
				switch fieldNum {
				case 2:
					acc.DisplayName = sVal
				case 3:
					acc.Email = sVal
				case 10:
					acc.GaiaID = sVal
				}
			default:
				subOffset = len(subData)
			}
		}
		if acc.Email != "" {
			accounts = append(accounts, acc)
			defaultIdx++
		}
	}
	return accounts
}
