const locales = {
    'zh-CN': {
        label: '简体中文',
        pageTitle: 'AI Studio 控制台',
        nav: {
            dashboard: '仪表盘',
            config: '配置',
            auth: '认证文件',
            system: '系统工具',
            playground: 'Playground'
        },
        status: {
            title: '服务状态',
            start: '启动服务',
            stop: '停止服务',
            loading: '加载中...',
            noModels: '无可用模型',
            loadFailed: '加载失败',
            stopped: '[已停止生成]'
        },
        logs: {
            level: '日志等级',
            clear: '清空日志',
            autoScroll: '自动滚动',
            waiting: '等待日志输出...',
            all: '全部',
            info: '信息',
            warn: '警告',
            error: '错误'
        },
        action: {
            title: '需要您的操作',
            placeholder: '输入自定义内容后按 Enter...',
            send: '发送',
            shortcuts: '快捷操作',
            sendEnter: '发送 Enter (空)',
            sendN: '发送 "N"',
            sendY: '发送 "y"',
            send1: '发送 "1"',
            send2: '发送 "2"'
        },
        config: {
            title: '启动配置',
            fastapiPort: 'FastAPI 服务端口',
            camoufoxPort: 'Camoufox 调试端口',
            default: '默认',
            launchMode: '启动模式',
            modeHeadless: '无头模式 (Headless) - 推荐，后台静默运行',
            modeDebug: '调试模式 (Debug) - 显示浏览器窗口，用于手动登录',
            modeVirtual: '虚拟显示模式 (Linux Xvfb)',
            modeDesc: '调试模式将弹出一个新的浏览器窗口。无头模式将在后台运行。',
            streamProxy: '流式代理服务',
            streamPort: '流式端口',
            httpProxy: 'HTTP 代理',
            proxyAddress: '代理地址',
            scriptInjection: '模型注入脚本',
            scriptInjectionDesc: '启用后可添加 AI Studio 未列出的模型（已被弃用）',
            logEnabled: '启用日志',
            logEnabledDesc: '禁用日志可提升性能（需重启服务生效）',
            workerStartupInterval: 'Worker 启动间隔（秒）',
            workerStartupIntervalDesc: '多个 Worker 之间依次启动的等待时间，默认 5 秒',
            save: '保存配置'
        },
        auth: {
            title: '认证文件管理',
            active: '当前激活',
            using: '正在使用此文件进行认证',
            deactivate: '取消激活',
            noActive: '当前无激活的认证文件',
            saved: '已保存文件 (Saved)',
            activate: '激活此文件',
            notFound: '没有找到已保存的认证文件'
        },
        system: {
            title: '系统工具',
            portStatus: '端口占用情况',
            refresh: '刷新',
            inUse: '被占用',
            free: '空闲',
            kill: '终止',
            portFree: '此端口当前未被占用',
            refreshHint: '点击刷新查看端口状态'
        },
        chat: {
            placeholder: '输入消息 (Ctrl+Enter 发送)...',
            send: '发送',
            stop: '停止',
            customModel: '自定义...',
            clear: '清空对话',
            start: '开始一个新的对话...',
            systemPrompt: '系统提示词',
            endpoint: 'API 地址',
            apiKey: 'API 密钥',
            model: '模型',
            temperature: '随机性 (Temperature)',
            topP: '核采样 (Top P)',
            maxTokens: '最大输出 Tokens',
            googleSearch: '谷歌搜索'
        },
        worker: {
            title: 'Worker 管理',
            mode: 'Worker 模式',
            modeDesc: '启用后，点击左下角"启动服务"将启动所有已添加的Worker（多账号并发），日志显示在主页',
            modeEnabled: '✓ Worker模式已启用，共{count}个Worker。点击左下角"启动服务"开始。',
            configured: '已配置的 Workers',
            saveConfig: '💾 保存配置',
            refresh: '刷新',
            noWorkers: '暂无Worker',
            addFromAuthFiles: '从下方认证文件列表添加Worker',
            port: '端口',
            requests: '请求',
            start: '启动',
            stop: '停止',
            clearLimits: '清除限流',
            delete: '删除',
            rateLimitedModels: '⚠️ 限流模型',
            activeAuth: '当前激活认证',
            deactivate: '停用',
            noActiveAuth: '无激活认证（单Worker模式使用）',
            authFileList: '认证文件列表',
            rename: '重命名',
            activate: '激活',
            added: '已添加',
            addAsWorker: '添加为Worker',
            noSavedAuthFiles: '暂无已保存的认证文件'
        },
        modal: {
            renameAuth: '重命名认证文件',
            oldName: '原文件名',
            newNamePlaceholder: '新文件名',
            cancel: '取消',
            confirm: '确认'
        },
        logs: {
            level: '日志等级',
            clear: '清空日志',
            autoScroll: '自动滚动',
            waiting: '等待日志输出...',
            all: '全部',
            info: '信息',
            warn: '警告',
            error: '错误',
            source: '来源',
            allSources: '全部'
        },
        confirm: {
            deactivateAuth: '确定要取消激活当前认证文件吗？',
            killProcess: '确定要强制终止进程 {pid} 吗？'
        },
        alert: {
            operationFailed: '操作失败',
            requestError: '请求发生错误',
            workerConfigSaved: 'Worker配置已保存'
        }
    },
    'zh-TW': {
        label: '繁體中文',
        pageTitle: 'AI Studio 控制台',
        nav: {
            dashboard: '儀表板',
            config: '設定',
            auth: '認證檔案',
            system: '系統工具',
            playground: 'Playground'
        },
        status: {
            title: '服務狀態',
            start: '啟動服務',
            stop: '停止服務',
            loading: '載入中...',
            noModels: '無可用模型',
            loadFailed: '載入失敗',
            stopped: '[已停止生成]'
        },
        logs: {
            level: '紀錄等級',
            clear: '清空紀錄',
            autoScroll: '自動捲動',
            waiting: '等待紀錄輸出...',
            all: '全部',
            info: '資訊',
            warn: '警告',
            error: '錯誤'
        },
        action: {
            title: '需要您的操作',
            placeholder: '輸入自定義內容後按 Enter...',
            send: '發送',
            shortcuts: '快捷操作',
            sendEnter: '發送 Enter (空)',
            sendN: '發送 "N"',
            sendY: '發送 "y"',
            send1: '發送 "1"',
            send2: '發送 "2"'
        },
        config: {
            title: '啟動設定',
            fastapiPort: 'FastAPI 服務埠',
            camoufoxPort: 'Camoufox 偵錯埠',
            default: '預設',
            launchMode: '啟動模式',
            modeHeadless: '無頭模式 (Headless) - 推薦，背景靜默執行',
            modeDebug: '偵錯模式 (Debug) - 顯示瀏覽器視窗，用於手動登入',
            modeVirtual: '虛擬顯示模式 (Linux Xvfb)',
            modeDesc: '偵錯模式將彈出一個新的瀏覽器視窗。無頭模式將在背景執行。',
            streamProxy: '流式代理服務',
            streamPort: '流式埠',
            httpProxy: 'HTTP 代理',
            proxyAddress: '代理位址',
            scriptInjection: '模型注入腳本',
            scriptInjectionDesc: '啟用後可添加 AI Studio 未列出的模型（已被棄用）',
            logEnabled: '啟用日誌',
            logEnabledDesc: '禁用日誌可提升性能（需重啟服務生效）',
            workerStartupInterval: 'Worker 啟動間隔（秒）',
            workerStartupIntervalDesc: '多個 Worker 之間依次啟動的等待時間，預設 5 秒',
            save: '儲存設定'
        },
        auth: {
            title: '認證檔案管理',
            active: '目前啟用',
            using: '正在使用此檔案進行認證',
            deactivate: '取消啟用',
            noActive: '目前無啟用的認證檔案',
            saved: '已儲存檔案 (Saved)',
            activate: '啟用此檔案',
            notFound: '沒有找到已儲存的認證檔案'
        },
        system: {
            title: '系統工具',
            portStatus: '埠佔用情況',
            refresh: '重新整理',
            inUse: '被佔用',
            free: '空閒',
            kill: '終止',
            portFree: '此埠目前未被佔用',
            refreshHint: '點擊重新整理查看埠狀態'
        },
        chat: {
            placeholder: '輸入訊息 (Ctrl+Enter 發送)...',
            send: '發送',
            stop: '停止',
            customModel: '自定義...',
            clear: '清空對話',
            start: '開始一個新的對話...',
            systemPrompt: '系統提示詞',
            endpoint: 'API 位址',
            apiKey: 'API 金鑰',
            model: '模型',
            temperature: '隨機性 (Temperature)',
            topP: '核取樣 (Top P)',
            maxTokens: '最大輸出 Tokens',
            googleSearch: 'Google 搜尋'
        },
        worker: {
            title: 'Worker 管理',
            mode: 'Worker 模式',
            modeDesc: '啟用後，點擊左下角「啟動服務」將啟動所有已添加的Worker（多帳號並發），日誌顯示在主頁',
            modeEnabled: '✓ Worker模式已啟用，共{count}個Worker。點擊左下角「啟動服務」開始。',
            configured: '已配置的 Workers',
            saveConfig: '💾 儲存配置',
            refresh: '重新整理',
            noWorkers: '暫無Worker',
            addFromAuthFiles: '從下方認證檔案列表添加Worker',
            port: '埠',
            requests: '請求',
            start: '啟動',
            stop: '停止',
            clearLimits: '清除限流',
            delete: '刪除',
            rateLimitedModels: '⚠️ 限流模型',
            activeAuth: '當前啟用認證',
            deactivate: '停用',
            noActiveAuth: '無啟用認證（單Worker模式使用）',
            authFileList: '認證檔案列表',
            rename: '重新命名',
            activate: '啟用',
            added: '已添加',
            addAsWorker: '添加為Worker',
            noSavedAuthFiles: '暫無已儲存的認證檔案'
        },
        modal: {
            renameAuth: '重新命名認證檔案',
            oldName: '原檔案名',
            newNamePlaceholder: '新檔案名',
            cancel: '取消',
            confirm: '確認'
        },
        logs: {
            level: '紀錄等級',
            clear: '清空紀錄',
            autoScroll: '自動捲動',
            waiting: '等待紀錄輸出...',
            all: '全部',
            info: '資訊',
            warn: '警告',
            error: '錯誤',
            source: '來源',
            allSources: '全部'
        },
        confirm: {
            deactivateAuth: '確定要取消啟用當前認證檔案嗎？',
            killProcess: '確定要強制終止進程 {pid} 嗎？'
        },
        alert: {
            operationFailed: '操作失敗',
            requestError: '請求發生錯誤',
            workerConfigSaved: 'Worker配置已儲存'
        }
    },
    en: {
        label: 'English',
        pageTitle: 'AI Studio Console',
        nav: {
            dashboard: 'Dashboard',
            config: 'Config',
            auth: 'Auth Files',
            system: 'System Tools',
            playground: 'Playground'
        },
        status: {
            title: 'Service Status',
            start: 'Start Service',
            stop: 'Stop Service',
            loading: 'Loading...',
            noModels: 'No models available',
            loadFailed: 'Load failed',
            stopped: '[Generation stopped]'
        },
        logs: {
            level: 'Log Level',
            clear: 'Clear Logs',
            autoScroll: 'Auto Scroll',
            waiting: 'Waiting for logs...',
            all: 'ALL',
            info: 'INFO',
            warn: 'WARN',
            error: 'ERROR'
        },
        action: {
            title: 'Action Required',
            placeholder: 'Type input and press Enter...',
            send: 'Send',
            shortcuts: 'Shortcuts',
            sendEnter: 'Send Enter (Empty)',
            sendN: 'Send "N"',
            sendY: 'Send "y"',
            send1: 'Send "1"',
            send2: 'Send "2"'
        },
        config: {
            title: 'Launch Configuration',
            fastapiPort: 'FastAPI Service Port',
            camoufoxPort: 'Camoufox Debug Port',
            default: 'Default',
            launchMode: 'Launch Mode',
            modeHeadless: 'Headless Mode - Recommended for background',
            modeDebug: 'Debug Mode - Shows browser window for manual login',
            modeVirtual: 'Virtual Display Mode (Linux Xvfb)',
            modeDesc: 'Debug mode pops up a new browser window. Headless mode runs in background.',
            streamProxy: 'Stream Proxy Service',
            streamPort: 'Stream Port',
            httpProxy: 'HTTP Proxy',
            proxyAddress: 'Proxy Address',
            scriptInjection: 'Model Injection Script',
            scriptInjectionDesc: 'Enable to add unlisted models in AI Studio (Deprecated)',
            logEnabled: 'Enable Logging',
            logEnabledDesc: 'Disabling logs improves performance (requires service restart)',
            workerStartupInterval: 'Worker Startup Interval (seconds)',
            workerStartupIntervalDesc: 'Delay between starting each Worker, default 5 seconds',
            save: 'Save Config'
        },
        auth: {
            title: 'Auth File Management',
            active: 'Currently Active',
            using: 'Using this file for authentication',
            deactivate: 'Deactivate',
            noActive: 'No active auth file',
            saved: 'Saved Files',
            activate: 'Activate',
            notFound: 'No saved auth files found'
        },
        system: {
            title: 'System Tools',
            portStatus: 'Port Usage',
            refresh: 'Refresh',
            inUse: 'In Use',
            free: 'Free',
            kill: 'Kill',
            portFree: 'Port is currently free',
            refreshHint: 'Click Refresh to view port status'
        },
        chat: {
            placeholder: 'Type a message (Ctrl+Enter to send)...',
            send: 'Send',
            stop: 'Stop',
            customModel: 'Custom...',
            clear: 'Clear Chat',
            start: 'Start a new conversation...',
            systemPrompt: 'System Prompt',
            endpoint: 'API Endpoint',
            apiKey: 'API Key',
            model: 'Model',
            temperature: 'Temperature',
            topP: 'Top P',
            maxTokens: 'Max Tokens',
            googleSearch: 'Google Search'
        },
        worker: {
            title: 'Worker Management',
            mode: 'Worker Mode',
            modeDesc: 'When enabled, clicking "Start Service" will launch all added Workers (multi-account concurrent), logs shown on dashboard',
            modeEnabled: '✓ Worker mode enabled, {count} Workers total. Click "Start Service" to begin.',
            configured: 'Configured Workers',
            saveConfig: '💾 Save Config',
            refresh: 'Refresh',
            noWorkers: 'No Workers',
            addFromAuthFiles: 'Add Workers from auth file list below',
            port: 'Port',
            requests: 'Requests',
            start: 'Start',
            stop: 'Stop',
            clearLimits: 'Clear Limits',
            delete: 'Delete',
            rateLimitedModels: '⚠️ Rate Limited Models',
            activeAuth: 'Active Authentication',
            deactivate: 'Deactivate',
            noActiveAuth: 'No active auth (single Worker mode)',
            authFileList: 'Auth File List',
            rename: 'Rename',
            activate: 'Activate',
            added: 'Added',
            addAsWorker: 'Add as Worker',
            noSavedAuthFiles: 'No saved auth files'
        },
        modal: {
            renameAuth: 'Rename Auth File',
            oldName: 'Original Name',
            newNamePlaceholder: 'New file name',
            cancel: 'Cancel',
            confirm: 'Confirm'
        },
        logs: {
            level: 'Log Level',
            clear: 'Clear Logs',
            autoScroll: 'Auto Scroll',
            waiting: 'Waiting for logs...',
            all: 'ALL',
            info: 'INFO',
            warn: 'WARN',
            error: 'ERROR',
            source: 'Source',
            allSources: 'All'
        },
        confirm: {
            deactivateAuth: 'Are you sure you want to deactivate the current auth file?',
            killProcess: 'Are you sure you want to force kill process {pid}?'
        },
        alert: {
            operationFailed: 'Operation Failed',
            requestError: 'Request Error',
            workerConfigSaved: 'Worker config saved'
        }
    },
    ja: {
        label: '日本語',
        pageTitle: 'AI Studio コンソール',
        nav: {
            dashboard: 'ダッシュボード',
            config: '設定',
            auth: '認証ファイル',
            system: 'システムツール',
            playground: 'プレイグラウンド'
        },
        status: {
            title: 'サービスステータス',
            start: 'サービス開始',
            stop: 'サービス停止',
            loading: '読み込み中...',
            noModels: '利用可能なモデルなし',
            loadFailed: '読み込み失敗',
            stopped: '[生成停止]'
        },
        logs: {
            level: 'ログレベル',
            clear: 'ログを消去',
            autoScroll: '自動スクロール',
            waiting: 'ログ出力を待機中...',
            all: 'すべて',
            info: '情報',
            warn: '警告',
            error: 'エラー'
        },
        action: {
            title: '操作が必要です',
            placeholder: '入力してEnterキーを押してください...',
            send: '送信',
            shortcuts: 'ショートカット',
            sendEnter: '送信 Enter (空)',
            sendN: '送信 "N"',
            sendY: '送信 "y"',
            send1: '送信 "1"',
            send2: '送信 "2"'
        },
        config: {
            title: '起動設定',
            fastapiPort: 'FastAPIポート',
            camoufoxPort: 'Camoufoxデバッグポート',
            default: 'デフォルト',
            launchMode: '起動モード',
            modeHeadless: 'ヘッドレスモード（バックグラウンド実行に推奨）',
            modeDebug: 'デバッグモード（手動ログイン用のブラウザを表示）',
            modeVirtual: '仮想ディスプレイモード (Linux Xvfb)',
            modeDesc: 'デバッグモードではブラウザウィンドウが表示されます。ヘッドレスモードはバックグラウンドで実行されます。',
            streamProxy: 'ストリームプロキシサービス',
            streamPort: 'ストリームポート',
            httpProxy: 'HTTPプロキシ',
            proxyAddress: 'プロキシアドレス',
            scriptInjection: 'モデル注入スクリプト',
            scriptInjectionDesc: '有効にするとAI Studioに未掲載のモデルを追加できます（非推奨）',
            logEnabled: 'ログを有効にする',
            logEnabledDesc: 'ログを無効にするとパフォーマンスが向上します（サービス再起動が必要）',
            workerStartupInterval: 'Worker起動間隔（秒）',
            workerStartupIntervalDesc: '各Workerの起動間隔、デフォルト5秒',
            save: '設定を保存'
        },
        auth: {
            title: '認証ファイル管理',
            active: '現在アクティブ',
            using: '認証に使用中',
            deactivate: '無効化',
            noActive: 'アクティブな認証ファイルはありません',
            saved: '保存済みファイル',
            activate: '有効化',
            notFound: '保存された認証ファイルが見つかりません'
        },
        system: {
            title: 'システムツール',
            portStatus: 'ポート使用状況',
            refresh: '更新',
            inUse: '使用中',
            free: '空き',
            kill: '終了',
            portFree: 'このポートは現在使用されていません',
            refreshHint: '更新をクリックしてステータスを確認'
        },
        chat: {
            placeholder: 'メッセージを入力 (Ctrl+Enterで送信)...',
            send: '送信',
            stop: '停止',
            customModel: 'カスタム...',
            clear: 'チャットをクリア',
            start: '新しい会話を開始...',
            systemPrompt: 'システムプロンプト',
            endpoint: 'APIエンドポイント',
            apiKey: 'APIキー',
            model: 'モデル',
            temperature: '温度 (Temperature)',
            topP: 'トップP (Top P)',
            maxTokens: '最大トークン数',
            googleSearch: 'Google検索'
        },
        worker: {
            title: 'Worker管理',
            mode: 'Workerモード',
            modeDesc: '有効にすると、「サービス開始」をクリックして追加されたすべてのWorkerを起動します（マルチアカウント並行）',
            modeEnabled: '✓ Workerモードが有効、{count}個のWorker。「サービス開始」をクリックして開始。',
            configured: '設定済みWorkers',
            saveConfig: '💾 設定を保存',
            refresh: '更新',
            noWorkers: 'Workerなし',
            addFromAuthFiles: '下の認証ファイルリストからWorkerを追加',
            port: 'ポート',
            requests: 'リクエスト',
            start: '開始',
            stop: '停止',
            clearLimits: '制限をクリア',
            delete: '削除',
            rateLimitedModels: '⚠️ レート制限モデル',
            activeAuth: 'アクティブな認証',
            deactivate: '無効化',
            noActiveAuth: 'アクティブな認証なし（単一Workerモード）',
            authFileList: '認証ファイルリスト',
            rename: '名前変更',
            activate: '有効化',
            added: '追加済み',
            addAsWorker: 'Workerとして追加',
            noSavedAuthFiles: '保存された認証ファイルなし'
        },
        modal: {
            renameAuth: '認証ファイル名変更',
            oldName: '元のファイル名',
            newNamePlaceholder: '新しいファイル名',
            cancel: 'キャンセル',
            confirm: '確認'
        },
        logs: {
            level: 'ログレベル',
            clear: 'ログを消去',
            autoScroll: '自動スクロール',
            waiting: 'ログ出力を待機中...',
            all: 'すべて',
            info: '情報',
            warn: '警告',
            error: 'エラー',
            source: 'ソース',
            allSources: 'すべて'
        },
        confirm: {
            deactivateAuth: '現在の認証ファイルを無効にしてもよろしいですか？',
            killProcess: 'プロセス {pid} を強制終了してもよろしいですか？'
        },
        alert: {
            operationFailed: '操作失敗',
            requestError: 'リクエストエラー',
            workerConfigSaved: 'Worker設定が保存されました'
        }
    },
    ko: {
        label: '한국어',
        pageTitle: 'AI Studio 콘솔',
        nav: {
            dashboard: '대시보드',
            config: '설정',
            auth: '인증 파일',
            system: '시스템 도구',
            playground: '플레이그라운드'
        },
        status: {
            title: '서비스 상태',
            start: '서비스 시작',
            stop: '서비스 중지',
            loading: '로딩 중...',
            noModels: '사용 가능한 모델 없음',
            loadFailed: '로드 실패',
            stopped: '[생성 중지됨]'
        },
        logs: {
            level: '로그 레벨',
            clear: '로그 지우기',
            autoScroll: '자동 스크롤',
            waiting: '로그 출력 대기 중...',
            all: '전체',
            info: '정보',
            warn: '경고',
            error: '오류'
        },
        action: {
            title: '작업 필요',
            placeholder: '입력 후 Enter를 누르세요...',
            send: '보내기',
            shortcuts: '단축키',
            sendEnter: '보내기 Enter (공백)',
            sendN: '보내기 "N"',
            sendY: '보내기 "y"',
            send1: '보내기 "1"',
            send2: '보내기 "2"'
        },
        config: {
            title: '시작 구성',
            fastapiPort: 'FastAPI 포트',
            camoufoxPort: 'Camoufox 디버그 포트',
            default: '기본값',
            launchMode: '시작 모드',
            modeHeadless: '헤드리스 모드 (백그라운드 실행 권장)',
            modeDebug: '디버그 모드 (수동 로그인을 위한 브라우저 표시)',
            modeVirtual: '가상 디스플레이 모드 (Linux Xvfb)',
            modeDesc: '디버그 모드는 새 브라우저 창을 띄웁니다. 헤드리스 모드는 백그라운드에서 실행됩니다.',
            streamProxy: '스트림 프록시 서비스',
            streamPort: '스트림 포트',
            httpProxy: 'HTTP 프록시',
            proxyAddress: '프록시 주소',
            scriptInjection: '모델 주입 스크립트',
            scriptInjectionDesc: '활성화하면 AI Studio에 나열되지 않은 모델 추가 가능 (더 이상 사용되지 않음)',
            logEnabled: '로깅 활성화',
            logEnabledDesc: '로그 비활성화 시 성능 향상 (서비스 재시작 필요)',
            workerStartupInterval: 'Worker 시작 간격 (초)',
            workerStartupIntervalDesc: '각 Worker 시작 사이의 대기 시간, 기본 5초',
            save: '설정 저장'
        },
        auth: {
            title: '인증 파일 관리',
            active: '현재 활성',
            using: '인증에 사용 중',
            deactivate: '비활성화',
            noActive: '활성 인증 파일 없음',
            saved: '저장된 파일',
            activate: '활성화',
            notFound: '저장된 인증 파일을 찾을 수 없습니다'
        },
        system: {
            title: '시스템 도구',
            portStatus: '포트 사용량',
            refresh: '새로 고침',
            inUse: '사용 중',
            free: '유휴',
            kill: '종료',
            portFree: '현재 포트가 사용되지 않습니다',
            refreshHint: '새로 고침을 클릭하여 상태 확인'
        },
        chat: {
            placeholder: '메시지 입력 (Ctrl+Enter 전송)...',
            send: '전송',
            stop: '중지',
            customModel: '사용자 정의...',
            clear: '대화 지우기',
            start: '새로운 대화 시작...',
            systemPrompt: '시스템 프롬프트',
            endpoint: 'API 엔드포인트',
            apiKey: 'API 키',
            model: '모델',
            temperature: '온도 (Temperature)',
            topP: 'Top P',
            maxTokens: '최대 토큰',
            googleSearch: 'Google 검색'
        },
        worker: {
            title: 'Worker 관리',
            mode: 'Worker 모드',
            modeDesc: '활성화하면 "서비스 시작"을 클릭하여 추가된 모든 Worker를 시작합니다 (다중 계정 동시 실행)',
            modeEnabled: '✓ Worker 모드 활성화됨, 총 {count}개 Worker. "서비스 시작"을 클릭하여 시작.',
            configured: '구성된 Workers',
            saveConfig: '💾 설정 저장',
            refresh: '새로 고침',
            noWorkers: 'Worker 없음',
            addFromAuthFiles: '아래 인증 파일 목록에서 Worker 추가',
            port: '포트',
            requests: '요청',
            start: '시작',
            stop: '중지',
            clearLimits: '제한 해제',
            delete: '삭제',
            rateLimitedModels: '⚠️ 속도 제한 모델',
            activeAuth: '활성 인증',
            deactivate: '비활성화',
            noActiveAuth: '활성 인증 없음 (단일 Worker 모드)',
            authFileList: '인증 파일 목록',
            rename: '이름 변경',
            activate: '활성화',
            added: '추가됨',
            addAsWorker: 'Worker로 추가',
            noSavedAuthFiles: '저장된 인증 파일 없음'
        },
        modal: {
            renameAuth: '인증 파일 이름 변경',
            oldName: '원래 이름',
            newNamePlaceholder: '새 파일 이름',
            cancel: '취소',
            confirm: '확인'
        },
        logs: {
            level: '로그 레벨',
            clear: '로그 지우기',
            autoScroll: '자동 스크롤',
            waiting: '로그 출력 대기 중...',
            all: '전체',
            info: '정보',
            warn: '경고',
            error: '오류',
            source: '소스',
            allSources: '전체'
        },
        confirm: {
            deactivateAuth: '현재 인증 파일을 비활성화하시겠습니까?',
            killProcess: '프로세스 {pid}를 강제 종료하시겠습니까?'
        },
        alert: {
            operationFailed: '작업 실패',
            requestError: '요청 오류',
            workerConfigSaved: 'Worker 설정 저장됨'
        }
    },
    fr: {
        label: 'Français',
        pageTitle: 'Console AI Studio',
        nav: {
            dashboard: 'Tableau de bord',
            config: 'Configuration',
            auth: 'Fichiers Auth',
            system: 'Outils Système',
            playground: 'Playground'
        },
        status: {
            title: 'État du service',
            start: 'Démarrer le service',
            stop: 'Arrêter le service',
            loading: 'Chargement...',
            noModels: 'Aucun modèle disponible',
            loadFailed: 'Échec du chargement',
            stopped: '[Génération arrêtée]'
        },
        logs: {
            level: 'Niveau de log',
            clear: 'Effacer les logs',
            autoScroll: 'Défilement auto',
            waiting: 'En attente de logs...',
            all: 'TOUT',
            info: 'INFO',
            warn: 'AVERT',
            error: 'ERREUR'
        },
        action: {
            title: 'Action requise',
            placeholder: 'Tapez et appuyez sur Entrée...',
            send: 'Envoyer',
            shortcuts: 'Raccourcis',
            sendEnter: 'Envoyer Entrée (Vide)',
            sendN: 'Envoyer "N"',
            sendY: 'Envoyer "y"',
            send1: 'Envoyer "1"',
            send2: 'Envoyer "2"'
        },
        config: {
            title: 'Configuration de lancement',
            fastapiPort: 'Port FastAPI',
            camoufoxPort: 'Port Debug Camoufox',
            default: 'Défaut',
            launchMode: 'Mode de lancement',
            modeHeadless: 'Mode Headless (Recommandé en arrière-plan)',
            modeDebug: 'Mode Debug (Affiche le navigateur)',
            modeVirtual: 'Mode Affichage Virtuel (Linux Xvfb)',
            modeDesc: 'Le mode Debug ouvre une fenêtre de navigateur. Le mode Headless s\'exécute en arrière-plan.',
            streamProxy: 'Service Proxy Stream',
            streamPort: 'Port Stream',
            httpProxy: 'Proxy HTTP',
            proxyAddress: 'Adresse Proxy',
            scriptInjection: 'Script d\'injection de modèle',
            scriptInjectionDesc: 'Activer pour ajouter des modèles non listés dans AI Studio (Obsolète)',
            logEnabled: 'Activer les journaux',
            logEnabledDesc: 'Désactiver améliore les performances (redémarrage requis)',
            save: 'Enregistrer'
        },
        auth: {
            title: 'Gestion des fichiers d\'authentification',
            active: 'Actuellement actif',
            using: 'Utilisé pour l\'authentification',
            deactivate: 'Désactiver',
            noActive: 'Aucun fichier actif',
            saved: 'Fichiers enregistrés',
            activate: 'Activer',
            notFound: 'Aucun fichier trouvé'
        },
        system: {
            title: 'Outils Système',
            portStatus: 'Utilisation des ports',
            refresh: 'Actualiser',
            inUse: 'Utilisé',
            free: 'Libre',
            kill: 'Tuer',
            portFree: 'Port actuellement libre',
            refreshHint: 'Cliquez sur Actualiser pour voir l\'état'
        },
        chat: {
            placeholder: 'Tapez un message (Ctrl+Entrée pour envoyer)...',
            send: 'Envoyer',
            stop: 'Arrêter',
            customModel: 'Personnalisé...',
            clear: 'Effacer',
            start: 'Démarrer une nouvelle conversation...',
            systemPrompt: 'Invite Système',
            endpoint: 'Endpoint API',
            apiKey: 'Clé API',
            model: 'Modèle',
            temperature: 'Température',
            topP: 'Top P',
            maxTokens: 'Tokens Max',
            googleSearch: 'Recherche Google'
        },
        worker: {
            title: 'Gestion Workers',
            mode: 'Mode Worker',
            modeDesc: 'Une fois activé, cliquez sur "Démarrer" pour lancer tous les Workers ajoutés (multi-compte simultané)',
            modeEnabled: '✓ Mode Worker activé, {count} Workers au total. Cliquez sur "Démarrer" pour commencer.',
            configured: 'Workers configurés',
            saveConfig: '💾 Enregistrer',
            refresh: 'Actualiser',
            noWorkers: 'Aucun Worker',
            addFromAuthFiles: 'Ajouter depuis la liste des fichiers auth',
            port: 'Port',
            requests: 'Requêtes',
            start: 'Démarrer',
            stop: 'Arrêter',
            clearLimits: 'Suppr. limites',
            delete: 'Supprimer',
            rateLimitedModels: '⚠️ Modèles limités',
            activeAuth: 'Auth active',
            deactivate: 'Désactiver',
            noActiveAuth: 'Pas d\'auth active (mode Worker unique)',
            authFileList: 'Liste fichiers auth',
            rename: 'Renommer',
            activate: 'Activer',
            added: 'Ajouté',
            addAsWorker: 'Ajouter comme Worker',
            noSavedAuthFiles: 'Aucun fichier auth sauvegardé'
        },
        modal: {
            renameAuth: 'Renommer fichier auth',
            oldName: 'Nom original',
            newNamePlaceholder: 'Nouveau nom',
            cancel: 'Annuler',
            confirm: 'Confirmer'
        },
        logs: {
            level: 'Niveau de log',
            clear: 'Effacer les logs',
            autoScroll: 'Défilement auto',
            waiting: 'En attente de logs...',
            all: 'TOUT',
            info: 'INFO',
            warn: 'AVERT',
            error: 'ERREUR',
            source: 'Source',
            allSources: 'Tout'
        },
        confirm: {
            deactivateAuth: 'Êtes-vous sûr de vouloir désactiver le fichier auth actuel ?',
            killProcess: 'Êtes-vous sûr de vouloir forcer l\'arrêt du processus {pid} ?'
        },
        alert: {
            operationFailed: 'Opération échouée',
            requestError: 'Erreur de requête',
            workerConfigSaved: 'Config Worker sauvegardée'
        }
    },
    de: {
        label: 'Deutsch',
        pageTitle: 'AI Studio Konsole',
        nav: {
            dashboard: 'Dashboard',
            config: 'Konfiguration',
            auth: 'Auth-Dateien',
            system: 'System-Tools',
            playground: 'Playground'
        },
        status: {
            title: 'Service-Status',
            start: 'Dienst starten',
            stop: 'Dienst stoppen',
            loading: 'Wird geladen...',
            noModels: 'Keine Modelle verfügbar',
            loadFailed: 'Laden fehlgeschlagen',
            stopped: '[Generierung gestoppt]'
        },
        logs: {
            level: 'Log-Level',
            clear: 'Logs löschen',
            autoScroll: 'Auto-Scroll',
            waiting: 'Warte auf Logs...',
            all: 'ALLE',
            info: 'INFO',
            warn: 'WARN',
            error: 'FEHLER'
        },
        action: {
            title: 'Aktion erforderlich',
            placeholder: 'Eingabe tippen und Enter drücken...',
            send: 'Senden',
            shortcuts: 'Shortcuts',
            sendEnter: 'Sende Enter (Leer)',
            sendN: 'Sende "N"',
            sendY: 'Sende "y"',
            send1: 'Sende "1"',
            send2: 'Sende "2"'
        },
        config: {
            title: 'Startkonfiguration',
            fastapiPort: 'FastAPI Port',
            camoufoxPort: 'Camoufox Debug Port',
            default: 'Standard',
            launchMode: 'Startmodus',
            modeHeadless: 'Headless-Modus (Empfohlen für Hintergrund)',
            modeDebug: 'Debug-Modus (Zeigt Browser für manuelle Anmeldung)',
            modeVirtual: 'Virtueller Display-Modus (Linux Xvfb)',
            modeDesc: 'Der Debug-Modus öffnet ein Browserfenster. Der Headless-Modus läuft im Hintergrund.',
            streamProxy: 'Stream Proxy Dienst',
            streamPort: 'Stream Port',
            httpProxy: 'HTTP Proxy',
            proxyAddress: 'Proxy Adresse',
            scriptInjection: 'Modell-Injektionsskript',
            scriptInjectionDesc: 'Aktivieren, um nicht aufgelistete Modelle in AI Studio hinzuzufügen (Veraltet)',
            logEnabled: 'Protokollierung aktivieren',
            logEnabledDesc: 'Deaktivieren verbessert die Leistung (Neustart erforderlich)',
            save: 'Speichern'
        },
        auth: {
            title: 'Auth-Dateiverwaltung',
            active: 'Aktuell aktiv',
            using: 'Wird zur Authentifizierung verwendet',
            deactivate: 'Deaktivieren',
            noActive: 'Keine aktive Auth-Datei',
            saved: 'Gespeicherte Dateien',
            activate: 'Aktivieren',
            notFound: 'Keine gespeicherten Dateien gefunden'
        },
        system: {
            title: 'System-Tools',
            portStatus: 'Port-Nutzung',
            refresh: 'Aktualisieren',
            inUse: 'Belegt',
            free: 'Frei',
            kill: 'Beenden',
            portFree: 'Port ist derzeit frei',
            refreshHint: 'Klicken Sie auf Aktualisieren für Status'
        },
        chat: {
            placeholder: 'Nachricht eingeben (Strg+Enter zum Senden)...',
            send: 'Senden',
            stop: 'Stopp',
            customModel: 'Benutzerdefiniert...',
            clear: 'Chat leeren',
            start: 'Neue Unterhaltung beginnen...',
            systemPrompt: 'System-Prompt',
            endpoint: 'API-Endpunkt',
            apiKey: 'API-Schlüssel',
            model: 'Modell',
            temperature: 'Temperatur',
            topP: 'Top P',
            maxTokens: 'Max Tokens',
            googleSearch: 'Google Suche'
        },
        worker: {
            title: 'Worker-Verwaltung',
            mode: 'Worker-Modus',
            modeDesc: 'Wenn aktiviert, klicken Sie auf "Dienst starten" um alle hinzugefügten Worker zu starten (Multi-Account parallel)',
            modeEnabled: '✓ Worker-Modus aktiviert, {count} Worker insgesamt. Klicken Sie auf "Dienst starten" zum Beginnen.',
            configured: 'Konfigurierte Workers',
            saveConfig: '💾 Speichern',
            refresh: 'Aktualisieren',
            noWorkers: 'Keine Worker',
            addFromAuthFiles: 'Worker aus Auth-Dateiliste hinzufügen',
            port: 'Port',
            requests: 'Anfragen',
            start: 'Starten',
            stop: 'Stoppen',
            clearLimits: 'Limits löschen',
            delete: 'Löschen',
            rateLimitedModels: '⚠️ Rate-limitierte Modelle',
            activeAuth: 'Aktive Authentifizierung',
            deactivate: 'Deaktivieren',
            noActiveAuth: 'Keine aktive Auth (Einzel-Worker-Modus)',
            authFileList: 'Auth-Dateiliste',
            rename: 'Umbenennen',
            activate: 'Aktivieren',
            added: 'Hinzugefügt',
            addAsWorker: 'Als Worker hinzufügen',
            noSavedAuthFiles: 'Keine gespeicherten Auth-Dateien'
        },
        modal: {
            renameAuth: 'Auth-Datei umbenennen',
            oldName: 'Ursprünglicher Name',
            newNamePlaceholder: 'Neuer Dateiname',
            cancel: 'Abbrechen',
            confirm: 'Bestätigen'
        },
        logs: {
            level: 'Log-Level',
            clear: 'Logs löschen',
            autoScroll: 'Auto-Scroll',
            waiting: 'Warte auf Logs...',
            all: 'ALLE',
            info: 'INFO',
            warn: 'WARN',
            error: 'FEHLER',
            source: 'Quelle',
            allSources: 'Alle'
        },
        confirm: {
            deactivateAuth: 'Sind Sie sicher, dass Sie die aktuelle Auth-Datei deaktivieren möchten?',
            killProcess: 'Sind Sie sicher, dass Sie Prozess {pid} zwangsbeenden möchten?'
        },
        alert: {
            operationFailed: 'Operation fehlgeschlagen',
            requestError: 'Anfragefehler',
            workerConfigSaved: 'Worker-Konfiguration gespeichert'
        }
    }
};

const useI18n = (ref) => {
    // 尝试从 localStorage 读取语言首选项，默认为 'zh-CN'
    const savedLang = localStorage.getItem('user_lang');
    const lang = ref(savedLang || 'zh-CN');

    const t = (key) => {
        const keys = key.split('.');
        let val = locales[lang.value];
        for (const k of keys) {
            val = val?.[k];
        }
        return val || key;
    };

    const setLang = (newLang) => {
        if (locales[newLang]) {
            lang.value = newLang;
            localStorage.setItem('user_lang', newLang);
        }
    };

    return {
        lang,
        t,
        setLang,
        availableLangs: Object.keys(locales).map(k => ({ code: k, label: locales[k].label }))
    };
};