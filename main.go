package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	// Secret Manager 的引用已被移除
	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

var (
	bot              *tgbotapi.BotAPI
	driveService     *drive.Service
	driveFolderID    string
	telegramBotToken string
)

// UPDATED: 初始化 Google Drive 服務
func initDriveService(ctx context.Context) error {
	// 從環境變數讀取設定
	driveFolderID = os.Getenv("GOOGLE_DRIVE_FOLDER_ID")
	if driveFolderID == "" {
		return fmt.Errorf("GOOGLE_DRIVE_FOLDER_ID environment variable not set")
	}

	// 直接從環境變數讀取 JSON 憑證內容
	credentialsJSON := os.Getenv("GOOGLE_CREDENTIALS_JSON")
	if credentialsJSON == "" {
		return fmt.Errorf("GOOGLE_CREDENTIALS_JSON environment variable not set")
	}

	// 使用金鑰建立 Drive 服務
	config, err := google.JWTConfigFromJSON([]byte(credentialsJSON), drive.DriveFileScope)
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

// 處理檔案上傳 (此函式未變更)
func handleFile(message *tgbotapi.Message) {
	var fileID string
	var fileName string

	if message.Document != nil {
		fileID = message.Document.FileID
		fileName = message.Document.FileName
	} else if len(message.Photo) > 0 {
		photo := message.Photo[len(message.Photo)-1]
		fileID = photo.FileID
		fileName = fmt.Sprintf("%s.jpg", fileID)
	} else {
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

// 輔助函式，用來回覆使用者 (此函式未變更)
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

	if update.Message.IsCommand() || update.Message.Text != "" {
		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)
		replyToUser(update.Message.Chat.ID, update.Message.MessageID, "這是一個 Echo Bot，請傳送檔案給我，我會幫您上傳到 Google Drive。")
	} else {
		handleFile(update.Message)
	}

	w.WriteHeader(http.StatusOK)
}

func main() {
	ctx := context.Background()

	telegramBotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	if telegramBotToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN environment variable not set")
	}
	var err error
	bot, err = tgbotapi.NewBotAPI(telegramBotToken)
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

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
