const messages = [
  ["正在加载…", "Loading…", "読み込み中…"],
  [
    "列表加载失败，请点击刷新重试。",
    "Could not load the list. Refresh to retry.",
    "一覧を読み込めません。更新して再試行してください。",
  ],
  ["操作记录", "Activity", "操作履歴"],
  [
    "查看 token 和 Web 权限的变更记录。",
    "Review token and Web permission changes.",
    "トークンと Web 権限の変更を確認します。",
  ],
  [
    "保留最近 1,000 条记录，包含操作时间、目标和结果；不记录 token 密钥。",
    "Keeps the latest 1,000 events with time, target and result. Token secrets are never recorded.",
    "日時、対象、結果を含む最新 1,000 件を保存します。トークンの秘密値は記録しません。",
  ],
  ["操作时间", "Time", "操作日時"],
  ["目标", "Target", "対象"],
  ["结果", "Result", "結果"],
  ["成功", "Succeeded", "成功"],
  ["失败", "Failed", "失敗"],
  ["暂无操作记录。", "No activity yet.", "操作履歴はありません。"],
  ["操作记录分页", "Activity pagination", "操作履歴のページ切り替え"],
  ["设备注册", "Device registration", "デバイス登録"],
  ["重新签发 token", "Reissue token", "トークンの再発行"],
  ["设备注销", "Device unregistration", "デバイス登録解除"],
  ["永久删除", "Delete permanently", "完全に削除"],
  [
    "永久删除记录？",
    "Delete records permanently?",
    "記録を完全に削除しますか？",
  ],
  [
    "将永久删除 {count} 条记录。",
    "Permanently delete {count} records.",
    "{count} 件の記録を完全に削除します。",
  ],
  [
    "已永久删除 {count} 条记录。",
    "Permanently deleted {count} records.",
    "{count} 件の記録を完全に削除しました。",
  ],
  [
    "此操作无法恢复，会断开当前连接并使旧 token 失效，同时移除这些记录的撤销限制和 Web 禁用状态。开启自动注册时，设备可能重新注册。",
    "This cannot be undone. Current connections will close and old tokens will stop working. Revocation restrictions and Web blocks held by these records will also be removed. Devices may enroll again if automatic registration is enabled.",
    "元に戻せません。現在の接続を切断し、古いトークンを無効にします。これらの記録による登録禁止と Web ブロックも解除されます。自動登録が有効な場合、デバイスが再登録される可能性があります。",
  ],
  [
    "输入 DELETE 确认永久删除",
    "Type DELETE to confirm permanent deletion",
    "完全削除を確認するには DELETE と入力",
  ],
  [
    "可跨页选择，单次最多选择 256 项。",
    "Select across pages, up to 256 items at a time.",
    "ページをまたいで最大 256 件を選択できます。",
  ],
  ["Spool Relay · 管理", "Spool Relay · Admin", "Spool Relay · 管理"],
  ["Spool Relay 首页", "Spool Relay home", "Spool Relay ホーム"],
  [
    "你的工作空间，始终相连。",
    "Your workspace. Always connected.",
    "いつでも、ワークスペースへ。",
  ],
  ["让工作，", "Your work, ", "どこからでも、"],
  ["随处可达。", "within reach.", "つながる。"],
  [
    "连接你的设备，回到熟悉的工作空间。",
    "Connect your devices. Return to your workspace.",
    "デバイスをつないで、いつもの作業場所へ。",
  ],
  [
    "让本地的 Spool，陪你走得更远。",
    "Keep your local Spool close, wherever you go.",
    "ローカルの Spool を、もっと身近に。",
  ],
  [
    "Spool Relay · 连接你的私有工作空间",
    "Spool Relay · Your private workspace, connected",
    "Spool Relay · プライベートな作業場所へ",
  ],
  ["欢迎回来", "Welcome back", "おかえりなさい"],
  ["登录 Relay 管理", "Sign in to Relay", "Relay 管理にログイン"],
  [
    "查看连接状态，管理 Spool 服务器授权。",
    "Monitor connections and manage Spool server access.",
    "接続状態と Spool サーバーの認証情報を管理します。",
  ],
  ["管理员 token", "Administrator token", "管理者トークン"],
  [
    "输入管理员 token",
    "Enter your administrator token",
    "管理者トークンを入力",
  ],
  ["进入管理", "Enter console", "管理画面へ"],
  [
    "使用部署时设置的管理员 token。",
    "Use the administrator token set during deployment.",
    "デプロイ時に設定した管理者トークンを使用します。",
  ],
  [
    "凭据仅保留在当前页面，刷新后需重新登录。",
    "Credentials stay on this page. Sign in again after a refresh.",
    "認証情報はこのページ内のみで保持されます。更新後は再ログインが必要です。",
  ],
  ["Relay 管理", "Relay settings", "Relay 管理"],
  ["服务", "Service", "サービス"],
  ["公网连接", "Public connection", "パブリック接続"],
  ["服务器授权", "Server access", "サーバー認証"],
  ["打开菜单", "Open menu", "メニューを開く"],
  ["关闭菜单", "Close menu", "メニューを閉じる"],
  ["退出管理", "Sign out", "ログアウト"],
  ["刷新", "Refresh", "更新"],
  [
    "查看 Relay 的公网入口与代理连接状态。",
    "View your Relay's public addresses and proxy status.",
    "Relay の公開アドレスとプロキシ接続状態を確認します。",
  ],
  [
    "管理 token 的生成、注册状态与访问权限。",
    "Generate tokens and manage their registration status and access.",
    "トークンの生成、登録状態、アクセス権限を管理します。",
  ],
  ["访问地址", "Addresses", "アクセス先"],
  ["由部署配置提供", "Set by deployment", "デプロイ設定から取得"],
  ["Relay 公网地址", "Relay public URL", "Relay 公開 URL"],
  [
    "Relay 服务的公网 HTTPS 入口。",
    "The public HTTPS entry point for this Relay.",
    "Relay サービスの公開 HTTPS エンドポイントです。",
  ],
  ["Web 子域名后缀", "Web subdomain suffix", "Web サブドメインのサフィックス"],
  [
    "服务器 Web 地址自动使用此后缀。",
    "Server Web addresses use this suffix automatically.",
    "サーバーの Web アドレスに自動で使用されます。",
  ],
  [
    "地址仅供查看。如需修改，请更新部署环境中的",
    "Addresses are read-only. To change them, update ",
    "アドレスは表示専用です。変更するには、デプロイ環境の",
  ],
  [
    "，并重新部署；Web 子域名后缀会自动推导。",
    " and redeploy. The Web suffix is derived automatically.",
    "を更新して再デプロイしてください。Web サフィックスは自動で決まります。",
  ],
  ["尚未配置", "Not configured", "未設定"],
  ["连接检测", "Connection check", "接続チェック"],
  ["尚未验证", "Not verified", "未検証"],
  ["检测", "Check", "チェック"],
  ["等待代理…", "Waiting…", "待機中…"],
  ["检测中…", "Checking…", "チェック中…"],
  ["等待代理就绪", "Waiting for proxy", "プロキシの準備待ち"],
  ["正在检测", "Checking connection", "接続をチェック中"],
  ["检测未通过", "Connection check failed", "接続チェック失敗"],
  ["公网连接正常", "Public connection ready", "パブリック接続は正常です"],
  [
    "正在等待公网 HTTPS 代理就绪，就绪后会自动验证…",
    "Waiting for the public HTTPS proxy. Verification will start automatically…",
    "公開 HTTPS プロキシの準備ができ次第、自動で検証します…",
  ],
  [
    "正在验证公网地址，成功后将自动保存并生效…",
    "Verifying the public address. The result will be saved automatically…",
    "公開アドレスを検証中です。成功すると自動で保存されます…",
  ],
  [
    "检测成功 · {proxy} · {date}",
    "Check passed · {proxy} · {date}",
    "検証成功 · {proxy} · {date}",
  ],
  ["已信任代理 {proxy}", "Trusted proxy {proxy}", "信頼済みプロキシ {proxy}"],
  [
    "无需配置可信代理",
    "No trusted proxy needed",
    "信頼済みプロキシの設定は不要です",
  ],
  [
    "尚未验证，点击检测重试。",
    "Not verified yet. Click Check to retry.",
    "未検証です。チェックを押して再試行してください。",
  ],
  [
    "请在部署环境中设置 RELAY_PUBLIC_URL，重新部署后会自动检测。",
    "Set RELAY_PUBLIC_URL and redeploy to start verification.",
    "RELAY_PUBLIC_URL を設定して再デプロイすると、自動検証が始まります。",
  ],
  ["自动检测的代理", "Detected proxy", "検出されたプロキシ"],
  [
    "验证成功后自动信任当前代理 IP。",
    "The verified proxy IP is trusted automatically.",
    "検証したプロキシ IP を自動で信頼します。",
  ],
  [
    "直接 TLS 连接，无需代理 IP",
    "Direct TLS connection; no proxy IP needed",
    "直接 TLS 接続のためプロキシ IP は不要です",
  ],
  ["尚无", "None", "なし"],
  [
    "启动时会等待公网 HTTPS 代理就绪并自动检测，也可以手动重试。",
    "Relay checks the public HTTPS proxy at startup. You can also retry manually.",
    "起動時に公開 HTTPS プロキシを自動検証します。手動での再試行も可能です。",
  ],
  ["高级设置", "Advanced settings", "詳細設定"],
  ["额外可信代理", "Additional trusted proxies", "追加の信頼済みプロキシ"],
  [
    "通常无需填写。多级代理可补充明确可信的 IP/CIDR，使用逗号分隔。",
    "Usually unnecessary. For multiple proxy layers, add trusted IPs/CIDRs separated by commas.",
    "通常は不要です。多段プロキシの場合、信頼する IP/CIDR をカンマで区切って追加します。",
  ],
  ["保存设置", "Save settings", "設定を保存"],
  ["连接概况", "Connection overview", "接続状況"],
  ["在线服务器", "Online servers", "オンラインサーバー"],
  ["有效 token", "Active tokens", "有効なトークン"],
  ["当前连接", "Connections", "接続数"],
  ["生成方式", "Generation mode", "生成方法"],
  ["单个生成", "Single", "1 件ずつ"],
  ["批量生成", "Batch", "一括生成"],
  ["Token 名称（选填）", "Token name (optional)", "トークン名（任意）"],
  [
    "留空时自动生成 12 位大小写字母名称。",
    "Leave blank for a random name of 12 uppercase and lowercase letters.",
    "空欄の場合、大文字・小文字の英字 12 文字で名前を生成します。",
  ],
  [
    "留空则随机生成名称",
    "Leave blank for a random name",
    "空欄でランダムな名前を生成",
  ],
  ["生成数量", "Quantity", "生成数"],
  [
    "每次可生成 1–100 个 token，名称随机使用 12 位大小写字母。",
    "Generate 1–100 tokens with random 12-letter names.",
    "英字 12 文字のランダムな名前で、1～100 件のトークンを生成します。",
  ],
  ["生成 token", "Generate token", "トークンを生成"],
  ["批量生成 token", "Generate tokens", "トークンを一括生成"],
  ["生成中…", "Generating…", "生成中…"],
  [
    "每个 token 只授权一台 Spool 服务器。",
    "Each token authorizes one Spool server.",
    "トークン 1 つにつき Spool サーバー 1 台を認証します。",
  ],
  [
    "已开启自动注册；新服务器也可凭签名身份自动注册。",
    "Automatic registration is enabled. New servers can also register using their signed identity.",
    "自動登録が有効です。新しいサーバーは署名済み ID でも登録できます。",
  ],
  [
    "本次会话生成的 token",
    "Tokens generated this session",
    "このセッションで生成したトークン",
  ],
  [
    "本页生成：{count} 个 token",
    "Session tokens: {count}",
    "このセッション：{count} 件のトークン",
  ],
  ["关闭 token", "Dismiss tokens", "トークンを閉じる"],
  ["关闭", "Close", "閉じる"],
  [
    "请及时复制或导出。本次生成的 token 仅保留在当前页面，关闭后无法再次查看。",
    "Copy or export now. These tokens stay on this page and cannot be retrieved after dismissal.",
    "今のうちにコピーまたはエクスポートしてください。閉じた後は再表示できません。",
  ],
  [
    "TXT 每行包含名称和 token。",
    "TXT: one name and token per line.",
    "TXT は 1 行に名前とトークンを含みます。",
  ],
  [
    "请导出 TXT 查看名称和 token。关闭页面后无法再次导出。",
    "Export TXT to view names and tokens. Export is unavailable after closing this page.",
    "名前とトークンは TXT で確認してください。ページを閉じた後は再エクスポートできません。",
  ],
  ["导出 TXT", "Export TXT", "TXT をエクスポート"],
  ["复制", "Copy", "コピー"],
  ["token 已复制。", "Token copied.", "トークンをコピーしました。"],
  [
    "请使用系统复制快捷键复制已选中的 token。",
    "Use your system's copy shortcut to copy the selected token.",
    "選択されたトークンをショートカットキーでコピーしてください。",
  ],
  ["Token 管理", "Token management", "トークン管理"],
  ["Token 名称", "Token name", "トークン名"],
  ["设备状态", "Device status", "デバイスの状態"],
  ["注册 IP", "Registration IP", "登録元 IP"],
  ["Web 状态", "Web status", "Web の状態"],
  ["操作", "Action", "操作"],
  ["搜索与筛选 token", "Search and filter tokens", "トークンの検索と絞り込み"],
  ["搜索 token", "Search tokens", "トークンを検索"],
  [
    "名称、Device ID 或注册 IP",
    "Name, Device ID or registration IP",
    "名前、Device ID、登録元 IP",
  ],
  ["连接状态", "Connection status", "接続状態"],
  ["全部连接状态", "All connections", "すべての接続"],
  ["全部 Web 状态", "All Web states", "すべての Web 状態"],
  ["Web 已发布", "Web published", "Web 公開中"],
  ["Web 已停用", "Web disabled", "Web 無効"],
  ["重置筛选", "Reset filters", "絞り込みをリセット"],
  [
    "没有匹配的 token，请调整搜索或筛选条件。",
    "No matching tokens. Adjust your search or filters.",
    "一致するトークンがありません。検索条件を変更してください。",
  ],
  ["全选本页", "Select page", "このページを選択"],
  ["已选 {count} 项", "{count} selected", "{count} 件選択中"],
  ["选择 {name}", "Select {name}", "{name} を選択"],
  ["清除选择", "Clear selection", "選択を解除"],
  ["Token 详情", "Token details", "トークンの詳細"],
  ["关闭详情", "Close details", "詳細を閉じる"],
  ["复制 Token ID", "Copy Token ID", "Token ID をコピー"],
  ["复制 Device ID", "Copy Device ID", "Device ID をコピー"],
  ["复制注册 IP", "Copy registration IP", "登録元 IP をコピー"],
  ["复制 Web 地址", "Copy Web URL", "Web URL をコピー"],
  ["Web 地址", "Web URL", "Web URL"],
  ["创建时间", "Created", "作成日時"],
  ["撤销时间", "Revoked", "取り消し日時"],
  ["注销时间", "Retired", "登録解除日時"],
  ["已复制。", "Copied.", "コピーしました。"],
  [
    "请使用系统复制快捷键复制已选中的内容。",
    "Use your system's copy shortcut to copy the selected text.",
    "選択したテキストをシステムのショートカットキーでコピーしてください。",
  ],
  [
    "将撤销 {count} 个 token，并断开它们的 Relay 连接。此操作无法撤回。",
    "Revoke {count} tokens and disconnect their Relay connections. This cannot be undone.",
    "{count} 件のトークンを取り消し、Relay 接続を切断します。この操作は元に戻せません。",
  ],
  [
    "将允许 {count} 台服务器发布 Web。服务器仍需自行开启 Web。",
    "Allow {count} servers to publish Web. Each server must still enable Web itself.",
    "{count} 台のサーバーに Web 公開を許可します。各サーバーでも Web を有効にする必要があります。",
  ],
  [
    "将停用 {count} 台服务器的 Web 访问。",
    "Disable Web access for {count} servers.",
    "{count} 台のサーバーの Web アクセスを無効にします。",
  ],
  [
    "成功 {success} 项，失败 {failed} 项。失败项保留选择，可重试。",
    "{success} succeeded, {failed} failed. Failed items remain selected for retry.",
    "{success} 件成功、{failed} 件失敗しました。失敗した項目は選択を維持して再試行できます。",
  ],
  [
    "已更新 {count} 项。",
    "Updated {count} items.",
    "{count} 件を更新しました。",
  ],
  ["Token 列表分页", "Token list pagination", "トークン一覧のページ切り替え"],
  ["上一页", "Previous", "前へ"],
  ["下一页", "Next", "次へ"],
  [
    "第 {start}–{end} 条，共 {total} 条",
    "Showing {start}–{end} of {total}",
    "{total} 件中 {start}～{end} 件",
  ],
  [
    "第 {page} / {pages} 页",
    "Page {page} of {pages}",
    "{page} / {pages} ページ",
  ],
  [
    "暂无已注册的 token。服务器使用 token 完成首次注册后会显示在这里。",
    "No registered tokens. Tokens appear here after a server completes its first registration.",
    "登録済みのトークンはありません。サーバーが初回登録を完了すると、ここに表示されます。",
  ],
  [
    "暂无已撤销或已注销的 token。",
    "No revoked or retired tokens.",
    "取り消し済み・登録解除済みのトークンはありません。",
  ],
  [
    "暂无未注册的 token。生成新 token 后会显示在这里。",
    "No unused tokens. New tokens will appear here.",
    "未登録のトークンはありません。新しく生成すると、ここに表示されます。",
  ],
  [
    "暂无 token。生成第一个 token 开始使用。",
    "No tokens yet. Generate a token to get started.",
    "トークンはまだありません。トークンを生成して開始してください。",
  ],
  [
    "撤销 token 会断开绑定服务器的 Relay 连接。已撤销列表也包含已注销的失效 token，记录会保留以防止旧凭据再次生效。",
    "Revoking a token disconnects its linked server. Revoked also includes retired tokens. Records are retained to prevent old credentials from being reused.",
    "トークンを取り消すと、登録サーバーの Relay 接続が切断されます。取り消し済みには登録解除済みの無効なトークンも含まれ、古い認証情報の再利用を防ぐため記録が保持されます。",
  ],
  ["未绑定服务器", "No linked server", "サーバー未登録"],
  [
    "等待服务器首次注册",
    "Awaiting first registration",
    "サーバーの初回登録待ち",
  ],
  ["Web 未发布", "Web not published", "Web 未公開"],
  ["创建于 {date}", "Created {date}", "作成日 {date}"],
  ["自动注册", "Automatic registration", "自動登録"],
  ["已注册", "Registered", "登録済み"],
  ["已撤销", "Revoked", "取り消し済み"],
  ["未注册", "Unused", "未登録"],
  ["全部", "All", "すべて"],
  ["已注销", "Retired", "登録解除済み"],
  ["在线", "Online", "オンライン"],
  ["离线", "Offline", "オフライン"],
  ["允许 Web", "Allow Web", "Web を許可"],
  ["停用 Web", "Disable Web", "Web を無効化"],
  ["撤销", "Revoke", "取り消す"],
  ["撤销 token？", "Revoke token?", "トークンを取り消しますか？"],
  [
    "即将撤销 token「{name}」。",
    "Token “{name}” will be revoked.",
    "トークン「{name}」を取り消します。",
  ],
  [
    "当前 Relay 连接会断开，旧 token 将无法重新连接。",
    "The Relay connection will close. The old token cannot reconnect.",
    "Relay 接続が切断され、古いトークンでは再接続できなくなります。",
  ],
  ["取消", "Cancel", "キャンセル"],
  ["撤销 token", "Revoke token", "トークンを取り消す"],
  ["语言与主题", "Appearance and language", "外観と言語"],
  ["关闭设置", "Close settings", "設定を閉じる"],
  ["主题", "Theme", "テーマ"],
  ["浅色", "Light", "ライト"],
  ["深色", "Dark", "ダーク"],
  ["跟随系统", "Follow system", "システム設定"],
  ["语言", "Language", "言語"],
  ["当前版本", "Current version", "現在のバージョン"],
  ["设置已保存。", "Settings saved.", "設定を保存しました。"],
  [
    "Web 发布资格已更新。",
    "Web publishing access updated.",
    "Web 公開の権限を更新しました。",
  ],
  ["token 已撤销。", "Token revoked.", "トークンを取り消しました。"],
  ["操作已取消。", "Operation cancelled.", "操作をキャンセルしました。"],
  [
    "管理员 token 无效，请重新输入。",
    "Invalid administrator token. Please try again.",
    "管理者トークンが無効です。再入力してください。",
  ],
  [
    "请求失败，请稍后重试。",
    "Request failed. Please try again.",
    "リクエストに失敗しました。再試行してください。",
  ],
  [
    "请使用 HTTPS 管理网址，或通过 SSH 将管理端口转发到本机后访问。管理员 token 需要安全的连接。",
    "Use HTTPS or an SSH tunnel to localhost. Administrator tokens require a secure connection.",
    "HTTPS または localhost への SSH トンネルを使用してください。管理者トークンには安全な接続が必要です。",
  ],
  [
    "count must be an integer between 1 and 100",
    "Quantity must be a whole number from 1 to 100.",
    "生成数は 1～100 の整数で指定してください。",
  ],
  [
    "a registration name of 1–128 bytes without control characters is required",
    "Names must be at most 128 UTF-8 bytes, without control characters.",
    "名前は制御文字を含めず、UTF-8 で 128 バイト以内にしてください。",
  ],
  [
    "registration storage unavailable",
    "Registration storage is unavailable.",
    "登録情報を保存できません。",
  ],
  [
    "settings storage unavailable",
    "Settings storage is unavailable.",
    "設定を保存できません。",
  ],
  [
    "invalid relay settings: check the HTTPS origin and proxy CIDRs",
    "Check the HTTPS origin and trusted proxy CIDRs.",
    "HTTPS オリジンとプロキシの CIDR を確認してください。",
  ],
  [
    "无法验证此地址。请检查 HTTPS 证书，并将检测路径转发到 Relay 服务端口（28080）。",
    "Could not verify the address. Check HTTPS certificates and route the probe to Relay port 28080.",
    "検証できませんでした。HTTPS 証明書と Relay ポート 28080 への転送設定を確認してください。",
  ],
  [
    "地址已到达 Relay，但转发信息不完整。请让代理设置 X-Forwarded-Proto、X-Forwarded-For，并保留公网 Host。",
    "Proxy headers are incomplete. Set X-Forwarded-Proto and X-Forwarded-For, and preserve the public Host.",
    "転送ヘッダーが不足しています。X-Forwarded-Proto と X-Forwarded-For を設定し、公開 Host を維持してください。",
  ],
  [
    "公网 HTTPS 代理尚未就绪。请检查代理、DNS 和证书，准备好后点击检测重试。",
    "The HTTPS proxy is not ready. Check the proxy, DNS and certificates, then retry.",
    "HTTPS プロキシの準備ができていません。プロキシ、DNS、証明書を確認して再試行してください。",
  ],
  ["检测已取消。", "Check cancelled.", "チェックをキャンセルしました。"],
  [
    "检测成功，但无法保存设置。请检查 Relay 数据目录权限。",
    "Verification passed but settings could not be saved. Check data directory permissions.",
    "検証に成功しましたが設定を保存できません。データディレクトリの権限を確認してください。",
  ],
];

const dictionary = new Map(messages.map((entry) => [entry[0], entry]));
for (const [key, chinese] of Object.entries({
  "count must be an integer between 1 and 100": "生成数量必须为 1–100 的整数。",
  "a registration name of 1–128 bytes without control characters is required":
    "名称不能超过 128 字节，且不能包含控制字符。",
  "registration storage unavailable":
    "无法保存授权记录，请检查 Relay 数据目录。",
  "settings storage unavailable": "无法保存设置，请检查 Relay 数据目录。",
  "invalid relay settings: check the HTTPS origin and proxy CIDRs":
    "请检查 HTTPS 地址与可信代理 CIDR。",
}))
  dictionary.get(key)[0] = chinese;
const storageKey = "spool.relay.ui-preferences.v1";
const systemTheme = matchMedia("(prefers-color-scheme: dark)");
let preferences = { language: "system", theme: "system" };
try {
  const saved = JSON.parse(localStorage.getItem(storageKey));
  if (["system", "zh-CN", "en", "ja"].includes(saved?.language))
    preferences.language = saved.language;
  if (["system", "light", "dark"].includes(saved?.theme))
    preferences.theme = saved.theme;
} catch {
  /* Browsers may block storage; preferences still work in memory. */
}

export function locale() {
  for (const language of preferences.language === "system"
    ? navigator.languages
    : [preferences.language]) {
    const base = language.toLowerCase().split(/[-_]/)[0];
    if (base === "zh") return "zh-CN";
    if (base === "en" || base === "ja") return base;
  }
  return "en";
}

export function t(key, values = {}) {
  const entry = dictionary.get(key);
  const translated = entry
    ? entry[["zh-CN", "en", "ja"].indexOf(locale())]
    : key;
  return translated.replace(/\{(\w+)\}/g, (match, name) =>
    String(values[name] ?? match),
  );
}

// Keep original keys for static DOM text so switching languages is reversible.
const dynamic = new Set([
  "token-list-status",
  "activity-rows",
  "activity-page-status",
  "activity-page-summary",
  "purge-description",
  "purge-items",
  "page-title",
  "page-description",
  "notice",
  "public-url",
  "web-domain",
  "detected-proxy",
  "detect",
  "detection-label",
  "detection-status",
  "registration-mode",
  "create-token",
  "token-result-title",
  "token-result-hint",
  "issued-results",
  "servers",
  "empty-state",
  "token-page-summary",
  "token-page-status",
  "token-selection-count",
  "token-details-name",
  "detail-device-status",
  "detail-web-status",
  "detail-copy-status",
  "bulk-dialog-title",
  "bulk-dialog-description",
  "bulk-dialog-items",
  "confirm-bulk-action",
  "revoke-description",
]);
const staticText = [];
const walker = document.createTreeWalker(document, NodeFilter.SHOW_TEXT);
while (walker.nextNode()) {
  const text = walker.currentNode;
  const key = text.textContent.trim().replace(/\s+/g, " ");
  if (!dictionary.has(key)) continue;
  let parent = text.parentElement;
  while (parent && !dynamic.has(parent.id)) parent = parent.parentElement;
  if (!parent) staticText.push({ text, key });
}
const staticAttributes = [];
for (const element of document.querySelectorAll(
  "[title], [aria-label], [placeholder]",
)) {
  for (const name of ["title", "aria-label", "placeholder"]) {
    const key = element.getAttribute(name);
    if (dictionary.has(key)) staticAttributes.push({ element, name, key });
  }
}

function apply() {
  document.documentElement.lang = locale();
  document.documentElement.dataset.theme =
    preferences.theme === "system"
      ? systemTheme.matches
        ? "dark"
        : "light"
      : preferences.theme;
  for (const { text, key } of staticText) text.textContent = t(key);
  for (const { element, name, key } of staticAttributes)
    element.setAttribute(name, t(key));
  document.getElementById("language-choice").value = preferences.language;
  for (const button of document.querySelectorAll("[data-theme-choice]"))
    button.setAttribute(
      "aria-pressed",
      String(button.dataset.themeChoice === preferences.theme),
    );
  document.dispatchEvent(new Event("relay-presentation-change"));
}

function save() {
  try {
    localStorage.setItem(storageKey, JSON.stringify(preferences));
  } catch {
    /* See above. */
  }
  apply();
}
document
  .getElementById("language-choice")
  .addEventListener("change", (event) => {
    preferences.language = event.target.value;
    save();
  });
for (const button of document.querySelectorAll("[data-theme-choice]"))
  button.addEventListener("click", () => {
    preferences.theme = button.dataset.themeChoice;
    save();
  });
const dialog = document.getElementById("appearance-dialog");
for (const id of ["login-preferences", "dashboard-preferences"])
  document
    .getElementById(id)
    .addEventListener("click", () => dialog.showModal());
document
  .getElementById("close-preferences")
  .addEventListener("click", () => dialog.close());
dialog.addEventListener("click", (event) => {
  if (event.target !== dialog) return;
  const rect = dialog.getBoundingClientRect();
  if (
    event.clientX < rect.left ||
    event.clientX > rect.right ||
    event.clientY < rect.top ||
    event.clientY > rect.bottom
  )
    dialog.close();
});
systemTheme.addEventListener("change", apply);
window.addEventListener("languagechange", apply);
apply();
