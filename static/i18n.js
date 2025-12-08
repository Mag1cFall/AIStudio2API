const locales = {
    'zh-CN': {
        label: '简体中文',
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
            stop: '停止服务'
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
        }
    },
    'zh-TW': {
        label: '繁體中文',
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
            stop: '停止服務'
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
        }
    },
    en: {
        label: 'English',
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
            stop: 'Stop Service'
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
        }
    },
    ja: {
        label: '日本語',
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
            stop: 'サービス停止'
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
        }
    },
    ko: {
        label: '한국어',
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
            stop: '서비스 중지'
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
        }
    },
    fr: {
        label: 'Français',
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
            stop: 'Arrêter le service'
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
        }
    },
    de: {
        label: 'Deutsch',
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
            stop: 'Dienst stoppen'
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