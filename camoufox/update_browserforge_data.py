from browserforge.download import Download, Remove, REMOTE_PATHS
REMOTE_PATHS['headers'] = 'https://raw.githubusercontent.com/apify/fingerprint-suite/667526247a519ec6fe7d99e640c45fbe403fb611/packages/header-generator/src/data_files'
REMOTE_PATHS['fingerprints'] = 'https://raw.githubusercontent.com/apify/fingerprint-suite/667526247a519ec6fe7d99e640c45fbe403fb611/packages/fingerprint-generator/src/data_files'
Remove()
Download(headers=True, fingerprints=True)