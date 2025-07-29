package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

var (
	bot               *tgbotapi.BotAPI
	driveService      *drive.Service
	driveFolderID     string
	telegramBotToken  string
	gcpProjectID      string
	credentialsSecret string
)

// NEW: 初始化 Google Drive 服務
func initDriveService(ctx context.Context) error {
	// 從環境變數讀取設定
	driveFolderID = os.Getenv("GOOGLE_DRIVE_FOLDER_ID")
	if driveFolderID == "" {
		return fmt.Errorf("GOOGLE_DRIVE_FOLDER_ID environment variable not set")
	}
	gcpProjectID = os.Getenv("GCP_PROJECT_ID")
	if gcpProjectID == "" {
		return fmt.Errorf("GCP_PROJECT_ID environment variable not set")
	}
	credentialsSecret = os.Getenv("CREDENTIALS_SECRET")
	if credentialsSecret == "" {
		return fmt.Errorf("CREDENTIALS_SECRET environment variable not set")
	}

	// 從 Secret Manager 獲取服務帳號金鑰
	secretName := fmt.Sprintf("projects/%s/secrets/%s/versions/latest", gcpProjectID, credentialsSecret)
	smClient, err := secretmanager.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create secretmanager client: %v", err)
	}
	defer smClient.Close()

	req := &secretmanagerpb.AccessSecretVersionRequest{
		Name: secretName,
	}
	result, err := smClient.AccessSecretVersion(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to access secret version: %v", err)
	}
	credentialsJSON := result.Payload.Data

	// 使用金鑰建立 Drive 服務
	config, err := google.JWTConfigFromJSON(credentialsJSON, drive.DriveFileScope)
	if err != nil {
		return fmt.Errorf("failed to create JWT config from JSON: %v", err)
	}

	client := config.Client(ctx)
	driveService, err = drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("failed to create drive service: %v", err)
	}
	return nil
}

// NEW: 處理檔案上傳
func handleFile(message *tgbotapi.Message) {
	var fileID string
	var fileName string

	if message.Document != nil {
		fileID = message.Document.FileID
		fileName = message.Document.FileName
	} else if len(message.Photo) > 0 {
		// 取最大尺寸的照片
		photo := message.Photo[len(message.Photo)-1]
		fileID = photo.FileID
		fileName = fmt.Sprintf("%s.jpg", fileID) // Telegram 照片沒有檔名，我們自己產生一個
	} else {
		return // 不是文件或照片
	}

	// 1. 從 Telegram 取得檔案下載連結
	fileURL, err := bot.GetFileDirectURL(fileID)
	if err != nil {
		log.Printf("Failed to get file URL: %v", err)
		replyToUser(message.Chat.ID, message.MessageID, "無法取得檔案，請稍後再試。")
		return
	}

	// 2. 下載檔案
	resp, err := http.Get(fileURL)
	if err != nil {
		log.Printf("Failed to download file: %v", err)
		replyToUser(message.Chat.ID, message.MessageID, "無法下載檔案，請稍後再試。")
		return
	}
	defer resp.Body.Close()

	// 3. 上傳到 Google Drive
	driveFile := &drive.File{
		Name:    fileName,
		Parents: []string{driveFolderID},
	}

	_, err = driveService.Files.Create(driveFile).Media(resp.Body).Do()
	if err != nil {
		log.Printf("Failed to upload to Drive: %v", err)
		replyToUser(message.Chat.ID, message.MessageID, "上傳到 Google Drive 失敗。")
		return
	}

	log.Printf("Successfully uploaded file '%s' to Drive.", fileName)
	replyToUser(message.Chat.ID, message.MessageID, fmt.Sprintf("檔案 '%s' 已成功上傳到 Google Drive！", fileName))
}

// 輔助函式，用來回覆使用者
func replyToUser(chatID int64, replyToMessageID int, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyToMessageID = replyToMessageID
	if _, err := bot.Send(msg); err != nil {
		log.Printf("could not send message: %v", err)
	}
}

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

	// 檢查是文字訊息還是檔案
	if update.Message.IsCommand() || update.Message.Text != "" {
		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)
		replyToUser(update.Message.Chat.ID, update.Message.MessageID, "這是一個 Echo Bot，請傳送檔案給我，我會幫您上傳到 Google Drive。")
	} else {
		// 是檔案，交給 handleFile 處理
		handleFile(update.Message)
	}

	w.WriteHeader(http.StatusOK)
}

func main() {
	ctx := context.Background()

	// 初始化 Telegram Bot
	telegramBotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	if telegramBotToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN environment variable not set")
	}
	var err error
	bot, err = tgbotapi.NewBotAPI(telegramBotToken)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	// 初始化 Google Drive 服務
	if err := initDriveService(ctx); err != nil {
		log.Fatalf("Failed to initialize Drive service: %v", err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", webhookHandler)

	log.Printf("starting server on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}