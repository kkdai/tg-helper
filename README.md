# TG Helper - Google Drive 上傳機器人

這是一個 Telegram 機器人，可以讓使用者授權自己的 Google Drive，然後將接收到的檔案（文件或圖片）自動上傳到使用者的 Google Drive 中。

## 功能

- **OAuth 2.0 授權**：透過標準的 Google OAuth 2.0 流程，讓使用者安全地授權，無需透露帳號密碼。
- **檔案上傳**：支援文件和圖片格式，直接上傳到授權使用者的 Google Drive 根目錄。
- **權杖管理**：使用 Google Firestore 安全地儲存每位使用者的 Refresh Token，以便在 Access Token 過期後能自動重新整理。
- **雲原生部署**：專為在 Google Cloud Run 上運行而設計，並可透過 Cloud Build 自動化部署。

## 技術架構

- **語言**: Go
- **Telegram API**: [go-telegram-bot-api/telegram-bot-api](https://github.com/go-telegram-bot-api/telegram-bot-api)
- **Google Cloud 服務**:
  - **Cloud Run**: 用於無伺服器運行 Web 服務。
  - **Cloud Build**: 用於自動化建置與部署。
  - **Firestore**: 用於儲存使用者授權權杖 (Token)。
  - **Google Drive API**: 用於上傳檔案。

---

## 設定與部署指南

在部署之前，您需要完成以下幾個設定步驟。

### 步驟 1：建立 Telegram Bot

1.  在 Telegram 中，找到 `@BotFather`。
2.  輸入 `/newbot` 指令，並依照指示為您的機器人命名。
3.  完成後，BotFather 會給您一組 **API Token**。請將此 Token 複製下來，稍後會用到。

### 步驟 2：設定 Google Cloud 專案

1.  **建立或選擇一個 Google Cloud 專案**。
2.  **啟用必要的 API**：在 Cloud Shell 或本地端執行以下指令，以確保所需服務皆已啟用。
    ```bash
    gcloud services enable \
      run.googleapis.com \
      cloudbuild.googleapis.com \
      drive.googleapis.com \
      firestore.googleapis.com
    ```

### 步驟 3：設定 Firestore 資料庫

1.  前往 [Google Cloud Firestore 主控台](https://console.cloud.google.com/firestore).
2.  點擊「**建立資料庫**」。
3.  選擇「**原生模式 (Native mode)**」。
4.  選擇一個離您使用者較近的區域位置。
5.  點擊「建立」。程式碼會自動處理集合 (Collection) 的建立，您無需手動操作。

### 步驟 4：設定 OAuth 2.0 憑證 (Client ID & Secret)

這是最關鍵的步驟，它讓您的應用程式能代表使用者請求 Google Drive 的存取權限。

1.  **設定 OAuth 同意畫面**:
    - 前往 [OAuth 同意畫面](https://console.cloud.google.com/apis/credentials/consent).
    - **使用者類型 (User Type)**: 選擇「**外部 (External)**」。
    - 填寫必要的應用程式資訊（應用程式名稱、使用者支援電子郵件等）。
    - **範圍 (Scopes)**: 點擊「新增或移除範圍」，找到並加入 `.../auth/drive.file`。這個範圍會將權限限制在「僅能存取由本應用程式建立的檔案」，是最安全的選項。
    - **測試使用者 (Test users)**: 在應用程式發布前，您必須將會用來測試的 Google 帳號（例如您自己的 Gmail）加入到測試使用者列表中。

2.  **建立 OAuth 2.0 用戶端 ID**:
    - 前往 [憑證頁面](https://console.cloud.google.com/apis/credentials).
    - 點擊「**+ 建立憑證**」 > 「**OAuth 用戶端 ID**」。
    - **應用程式類型**: 選擇「**網頁應用程式 (Web application)**」。
    - **已授權的重新導向 URI (Authorized redirect URIs)**:
      - 點擊「**+ 新增 URI**」。
      - 輸入您的 Cloud Run 服務的回呼網址。格式為 `https://<您的 Cloud Run 服務名稱>-<隨機字串>-<區域>.a.run.app/oauth/callback`。
      - **注意**：您需要先部署一次服務才能取得這個 URL。您可以先隨意填寫一個（例如 `http://localhost`），部署成功後再回來修改成正確的 URL。
    - 點擊「建立」。

3.  **複製 Client ID 和 Client Secret**:
    - 建立成功後，一個對話框會顯示您的「**用戶端 ID (Client ID)**」和「**用戶端密鑰 (Client Secret)**」。
    - **請務必將這兩組值複製並妥善保管**，它們將在部署時作為環境變數使用。

### 步驟 5：部署到 Cloud Run

我們推薦使用 Cloud Build 的 GitHub 觸發器來自動化部署。

1.  **連接 GitHub 儲存庫**：前往 Cloud Build 的「觸發器」頁面，並將此專案的 GitHub 儲存庫連接到您的 Google Cloud 專案。
2.  **建立觸發器**:
    - 建立一個新的推送 (Push) 觸發器。
    - 在「**進階**」 > 「**替代變數**」區塊，新增以下幾個變數。這是將您的密鑰安全地傳遞給建置流程的方法。

| 變數名稱 | 說明 | 範例 |
| :--- | :--- | :--- |
| `_TELEGRAM_BOT_TOKEN` | 您從 BotFather 取得的 API Token。 | `123456:ABC-DEF1234...` |
| `_GCP_PROJECT_ID` | 您的 Google Cloud 專案 ID。 | `my-gcp-project-123` |
| `_GOOGLE_CLIENT_ID` | 您在步驟 4-3 取得的用戶端 ID。 | `12345...apps.googleusercontent.com` |
| `_GOOGLE_CLIENT_SECRET` | 您在步驟 4-3 取得的用戶端密鑰。 | `GOCSPX-...` |
| `_GOOGLE_REDIRECT_URL` | 您在步驟 4-2 設定的回呼網址。 | `https://tg-helper-....a.run.app/oauth/callback` |

3.  **觸發部署**：將您的程式碼推送到 GitHub，Cloud Build 將會自動抓取、建置 Docker 映像檔，並將其部署到 Cloud Run，同時注入您設定的環境變數。

### 步驟 6：設定 Telegram Webhook

部署成功後，您需要告訴 Telegram 將所有訊息都發送到您的 Cloud Run 服務。請執行以下 `curl` 指令，並替換您的變數：

```bash
curl "https://api.telegram.org/bot<YOUR_TELEGRAM_BOT_TOKEN>/setWebhook?url=https://<YOUR_CLOUD_RUN_URL>"
```

如果看到 `{"ok":true,"result":true,"description":"Webhook was set"}` 的回應，就代表設定成功了！

## 如何使用

1.  在 Telegram 中找到您的機器人。
2.  發送 `/start` 或 `/connect_drive` 指令。
3.  機器人會回傳一個 Google 授權連結。
4.  點擊連結，登入您的 Google 帳號並同意授權。
5.  完成後，您就可以直接傳送任何檔案或圖片給機器人，它會自動將檔案上傳到您的 Google Drive。
