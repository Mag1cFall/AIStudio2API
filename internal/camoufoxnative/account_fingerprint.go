package camoufoxnative

// AccountFingerprint 返回账户固定 Camoufox 指纹配置，不存在时按当前版本生成并保存
func AccountFingerprint(options Options) (map[string]any, error) {
	return loadAccountCamoufoxConfig(options.StorageStatePath, camoufoxFirefoxMajor, options.Locale, options.Timezone)
}
