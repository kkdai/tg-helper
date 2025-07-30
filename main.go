package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- 全域變數 ---
var (
	bot             *tgbotapi.BotAPI
	oauth2Config    *oauth2.Config
	firestoreClient *firestore.Client
	gcpProjectID    string
)

const (
	// Firestore 集合名稱
	tokenCollection = "user_tokens"
	stateCollection = "oauth_states"
)

// UserToken 用來儲存在 Firestore 中的使用者權杖
type UserToken struct {
	UserID       int64         `firestore:"user_id"`
	RefreshToken string        `firestore:"refresh_token"`
	TokenType    string        `firestore:"token_type"`
	Expiry       time.Time     `firestore:"expiry"`
	AccessToken  string        `firestore:"access_token"`
	CreatedAt    time.Time     `firestore:"created_at"`
}

// --- 初始化 ---
func initFirestore(ctx context.Context) error {
	gcpProjectID = os.Getenv("GCP_PROJECT_ID")
	if gcpProjectID == "" {
		return fmt.Errorf("GCP_PROJECT_ID environment variable not set")
	}

	var err error
	firestoreClient, err = firestore.NewClient(ctx, gcpProjectID)
	if err != nil {
		return fmt.Errorf("failed to create firestore client: %v", err)
	}
	return nil
}

func initOAuth2Config() error {
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")
	redirectURL := os.Getenv("GOOGLE_REDIRECT_URL") // e.g., https://your-service.run.app/oauth/callback

	if clientID == "" || clientSecret == "" || redirectURL == "" {
		return fmt.Errorf("GOOGLE_CLIENT_ID, GOOGLE_CLIENT_SECRET, or GOOGLE_REDIRECT_URL not set")
	}

	oauth2Config = &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       []string{drive.DriveFileScope}, // 只要求上傳權限
		Endpoint:     google.Endpoint,
	}
	return nil
}

// --- 主要邏輯 ---

// 處理 /connect_drive 指令
func handleConnectDrive(message *tgbotapi.Message) {
	// 產生一個隨機的 state 字串來防止 CSRF 攻擊
	b := make([]byte, 32)
	rand.Read(b)
	state := base64.URLEncoding.EncodeToString(b)

	// 將 state 和使用者 ID 存到 Firestore，設定一個短的過期時間
	ctx := context.Background()
	_, err := firestoreClient.Collection(stateCollection).Doc(state).Set(ctx, map[string]interface{}{
		"user_id":    message.From.ID,
		"created_at": time.Now(),
	})
	if err != nil {
		log.Printf("Failed to save state to firestore: %v", err)
		replyToUser(message.Chat.ID, message.MessageID, "產生授權連結時發生錯誤，請稍後再試。")
		return
	}

	// 產生授權 URL
	authURL := oauth2Config.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	replyToUser(message.Chat.ID, message.MessageID, "請點擊以下連結授權本 Bot 存取您的 Google Drive (僅限上傳權限)：\n\n"+authURL)
}

// 處理來自 Google 的 OAuth 回呼
func oauthCallbackHandler(w http.ResponseWriter, r *http.Request) {
	ctx := context.Background()
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")

	// 1. 驗證 state
	doc, err := firestoreClient.Collection(stateCollection).Doc(state).Get(ctx)
	if err != nil {
		http.Error(w, "Invalid state parameter. Please try again.", http.StatusBadRequest)
		return
	}
	// 驗證後立即刪除 state，防止重複使用
	defer doc.Ref.Delete(ctx)

	var stateData struct {
		UserID int64 `firestore:"user_id"`
	}
	doc.DataTo(&stateData)
	userID := stateData.UserID

	// 2. 用授權碼交換權杖
	token, err := oauth2Config.Exchange(ctx, code)
	if err != nil {
		log.Printf("Failed to exchange token: %v", err)
		http.Error(w, "Failed to exchange token.", http.StatusInternalServerError)
		return
	}

	// 3. 將 Refresh Token 存到 Firestore
	userToken := &UserToken{
		UserID:       userID,
		RefreshToken: token.RefreshToken,
		AccessToken:  token.AccessToken,
		TokenType:    token.TokenType,
		Expiry:       token.Expiry,
		CreatedAt:    time.Now(),
	}

	// 使用 UserID 作為文件 ID
	_, err = firestoreClient.Collection(tokenCollection).Doc(fmt.Sprintf("%d", userID)).Set(ctx, userToken)
	if err != nil {
		log.Printf("Failed to save token to firestore: %v", err)
		http.Error(w, "Failed to save token.", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully saved token for user %d", userID)
	fmt.Fprintf(w, "授權成功！您現在可以回到 Telegram 傳送檔案給機器人了。")
}

// 處理檔案上傳
func handleFile(message *tgbotapi.Message) {
	ctx := context.Background()
	userID := message.From.ID

	// 1. 從 Firestore 取得使用者的權杖
	doc, err := firestoreClient.Collection(tokenCollection).Doc(fmt.Sprintf("%d", userID)).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			log.Printf("Token not found for user %d: %v", userID, err)
			replyToUser(message.Chat.ID, message.MessageID, "您的 Google Drive 帳號尚未連結，請使用 /connect_drive 指令來重新連結。")
		} else {
			log.Printf("Failed to retrieve token for user %d: %v", userID, err)
			replyToUser(message.Chat.ID, message.MessageID, "讀取您的授權時發生錯誤，請稍後再試。")
		}
		return
	}

	var userToken UserToken
	doc.DataTo(&userToken)

	// 2. 建立一個使用使用者權杖的 HTTP client
	token := &oauth2.Token{
		AccessToken:  userToken.AccessToken,
		TokenType:    userToken.TokenType,
		RefreshToken: userToken.RefreshToken,
		Expiry:       userToken.Expiry,
	}
	client := oauth2Config.Client(ctx, token)

	// 3. 建立 Drive 服務
	driveService, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Printf("Failed to create drive service for user %d: %v", userID, err)
		replyToUser(message.Chat.ID, message.MessageID, "建立 Google Drive 連線時發生錯誤。")
		return
	}

	// --- 以下與之前的檔案上傳邏輯相同 ---
	var fileID string
	var fileName string
	var fileSize int64 // 使用 int64 來儲存檔案大小

	if message.Document != nil {
		fileID, fileName, fileSize = message.Document.FileID, message.Document.FileName, int64(message.Document.FileSize)
	} else if len(message.Photo) > 0 {
		photo := message.Photo[len(message.Photo)-1]
		fileID, fileName, fileSize = photo.FileID, fmt.Sprintf("%s.jpg", photo.FileID), int64(photo.FileSize)
	} else {
		return
	}

	// 新增：檢查檔案大小是否超過 Telegram Bot API 的 20MB 下載限制
	const maxFileSize = 20 * 1024 * 1024 // 20 MB
	if fileSize > maxFileSize {
		log.Printf("File size %d exceeds the 20MB limit for user %d.", fileSize, userID)
		replyToUser(message.Chat.ID, message.MessageID, fmt.Sprintf("檔案大小為 %.2f MB，已超過 Telegram 機器人 20 MB 的下載限制，無法處理。", float64(fileSize)/1024/1024))
		return
	}

	fileURL, err := bot.GetFileDirectURL(fileID)
	if err != nil {
		log.Printf("Failed to get file URL: %v", err)
		replyToUser(message.Chat.ID, message.MessageID, "無法取得檔案，請稍後再試。")
		return
	}

	resp, err := http.Get(fileURL)
	if err != nil {
		log.Printf("Failed to download file: %v", err)
		replyToUser(message.Chat.ID, message.MessageID, "無法下載檔案，請稍後再試。")
		return
	}
	defer resp.Body.Close()

	// 注意：這裡不再需要 Parents，因為檔案會直接上傳到使用者的 "My Drive"
	driveFile := &drive.File{Name: fileName}

	_, err = driveService.Files.Create(driveFile).Media(resp.Body).Do()
	if err != nil {
		log.Printf("Failed to upload to Drive for user %d: %v", userID, err)
		replyToUser(message.Chat.ID, message.MessageID, "上傳到您的 Google Drive 失敗。")
		return
	}

	log.Printf("Successfully uploaded file '%s' to Drive for user %d.", fileName, userID)
	replyToUser(message.Chat.ID, message.MessageID, fmt.Sprintf("檔案 '%s' 已成功上傳到您的 Google Drive！", fileName))
}

// --- Webhook 和主函式 ---
func webhookHandler(w http.ResponseWriter, r *http.Request) {
	var update tgbotapi.Update
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		log.Printf("could not decode incoming update: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if update.Message == nil {
		w.WriteHeader(http.StatusOK)
		return
	}

	if update.Message.IsCommand() {
		switch update.Message.Command() {
		case "start":
			replyToUser(update.Message.Chat.ID, update.Message.MessageID, "歡迎使用！請使用 /connect_drive 來授權 Google Drive。")
		case "connect_drive":
			handleConnectDrive(update.Message)
		default:
			replyToUser(update.Message.Chat.ID, update.Message.MessageID, "無法辨識的指令。")
		}
	} else if update.Message.Document != nil || len(update.Message.Photo) > 0 {
		handleFile(update.Message)
	} else {
		replyToUser(update.Message.Chat.ID, update.Message.MessageID, "請傳送檔案或使用 /connect_drive 指令。")
	}

	w.WriteHeader(http.StatusOK)
}

func main() {
	ctx := context.Background()
	log.Println("Starting bot application with OAuth flow...")

	var err error
	bot, err = tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if err != nil {
		log.Fatalf("FATAL: Failed to create bot API: %v", err)
	}

	if err := initFirestore(ctx); err != nil {
		log.Fatalf("FATAL: Failed to initialize Firestore: %v", err)
	}

	if err := initOAuth2Config(); err != nil {
		log.Fatalf("FATAL: Failed to initialize OAuth2 config: %v", err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// 新增 /oauth/callback 路由
	http.HandleFunc("/oauth/callback", oauthCallbackHandler)
	// Telegram Webhook 路由
	http.HandleFunc("/", webhookHandler)

	log.Printf("Server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("FATAL: failed to start server: %v", err)
	}
}

func replyToUser(chatID int64, replyToMessageID int, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyToMessageID = replyToMessageID
	if _, err := bot.Send(msg); err != nil {
		log.Printf("ERROR: could not send reply message: %v", err)
	}
}
