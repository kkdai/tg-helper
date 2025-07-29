package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi" // 引入 googleapi 套件來解析詳細錯誤
	"google.golang.org/api/option"
)

var (
	bot              *tgbotapi.BotAPI
	driveService     *drive.Service
	driveFolderID    string
	telegramBotToken string
)

// UPDATED: initDriveService with more logging
func initDriveService(ctx context.Context) error {
	log.Println("Initializing Google Drive service...")
	driveFolderID = os.Getenv("GOOGLE_DRIVE_FOLDER_ID")
	if driveFolderID == "" {
		return fmt.Errorf("FATAL: GOOGLE_DRIVE_FOLDER_ID environment variable not set")
	}
	log.Printf("Using Google Drive Folder ID: %s", driveFolderID)

	credentialsJSON := os.Getenv("GOOGLE_CREDENTIALS_JSON")
	if credentialsJSON == "" {
		return fmt.Errorf("FATAL: GOOGLE_CREDENTIALS_JSON environment variable not set")
	}
	log.Println("Successfully loaded credentials from environment variable.")

	config, err := google.JWTConfigFromJSON([]byte(credentialsJSON), drive.DriveFileScope)
	if err != nil {
		return fmt.Errorf("failed to create JWT config from JSON: %v", err)
	}

	client := config.Client(ctx)
	driveService, err = drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("failed to create drive service: %v", err)
	}

	log.Println("Google Drive service initialized successfully.")
	return nil
}

// UPDATED: handleFile with more logging
func handleFile(message *tgbotapi.Message) {
	var fileID string
	var fileName string

	if message.Document != nil {
		log.Printf("Received a document from user %s. FileName: %s", message.From.UserName, message.Document.FileName)
		fileID = message.Document.FileID
		fileName = message.Document.FileName
	} else if len(message.Photo) > 0 {
		photo := message.Photo[len(message.Photo)-1]
		log.Printf("Received a photo from user %s. FileID: %s", message.From.UserName, photo.FileID)
		fileID = photo.FileID
		fileName = fmt.Sprintf("%s.jpg", fileID)
	} else {
		log.Println("Received a message that is not a document or photo. Ignoring.")
		return
	}

	log.Printf("Step 1: Getting file direct URL for FileID: %s", fileID)
	fileURL, err := bot.GetFileDirectURL(fileID)
	if err != nil {
		log.Printf("ERROR: Failed to get file URL: %v", err)
		replyToUser(message.Chat.ID, message.MessageID, "無法取得檔案，請稍後再試。")
		return
	}
	log.Printf("Step 1 Success: Got file URL: %s", fileURL)

	log.Printf("Step 2: Downloading file from Telegram...")
	resp, err := http.Get(fileURL)
	if err != nil {
		log.Printf("ERROR: Failed to download file: %v", err)
		replyToUser(message.Chat.ID, message.MessageID, "無法下載檔案，請稍後再試。")
		return
	}
	defer resp.Body.Close()
	log.Printf("Step 2 Success: File downloaded. HTTP Status: %s", resp.Status)

	if resp.StatusCode != http.StatusOK {
		log.Printf("ERROR: Telegram returned non-200 status code: %s", resp.Status)
		replyToUser(message.Chat.ID, message.MessageID, "從 Telegram 下載檔案時發生錯誤。")
		return
	}

	driveFile := &drive.File{
		Name:    fileName,
		Parents: []string{driveFolderID},
	}

	log.Printf("Step 3: Uploading file '%s' to Drive Folder ID '%s'", fileName, driveFolderID)
	_, err = driveService.Files.Create(driveFile).Media(resp.Body).Do()
	if err != nil {
		// 新增的詳細錯誤日誌
		log.Printf("ERROR: Failed to upload to Drive: %v", err)
		if gerr, ok := err.(*googleapi.Error); ok {
			log.Printf("Google API Error Details: Code=%d, Message=%s", gerr.Code, gerr.Message)
			for _, e := range gerr.Errors {
				log.Printf("Domain: %s, Reason: %s, Message: %s", e.Domain, e.Reason, e.Message)
			}
		}
		replyToUser(message.Chat.ID, message.MessageID, "上傳到 Google Drive 失敗。")
		return
	}

	log.Printf("Step 3 Success: Successfully uploaded file '%s' to Drive.", fileName)
	replyToUser(message.Chat.ID, message.MessageID, fmt.Sprintf("檔案 '%s' 已成功上傳到 Google Drive！", fileName))
}

func replyToUser(chatID int64, replyToMessageID int, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyToMessageID = replyToMessageID
	if _, err := bot.Send(msg); err != nil {
		log.Printf("ERROR: could not send reply message: %v", err)
	}
}

func webhookHandler(w http.ResponseWriter, r *http.Request) {
	var update tgbotapi.Update
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		log.Printf("ERROR: could not decode incoming update: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	if update.Message == nil {
		log.Println("Received an update with no message. Ignoring.")
		w.WriteHeader(http.StatusOK)
		return
	}

	log.Printf("Received message from @%s (ChatID: %d)", update.Message.From.UserName, update.Message.Chat.ID)

	if update.Message.IsCommand() || update.Message.Text != "" {
		log.Printf("Message is a text or command: '%s'", update.Message.Text)
		replyToUser(update.Message.Chat.ID, update.Message.MessageID, "這是一個 Echo Bot，請傳送檔案給我，我會幫您上傳到 Google Drive。")
	} else {
		log.Println("Message is a file/photo, processing with handleFile...")
		handleFile(update.Message)
	}

	w.WriteHeader(http.StatusOK)
}

func main() {
	ctx := context.Background()

	log.Println("Starting bot application...")

	telegramBotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	if telegramBotToken == "" {
		log.Fatal("FATAL: TELEGRAM_BOT_TOKEN environment variable not set")
	}
	var err error
	bot, err = tgbotapi.NewBotAPI(telegramBotToken)
	if err != nil {
		log.Fatalf("FATAL: Failed to create bot API: %v", err)
	}
	log.Println("Telegram Bot API initialized successfully.")

	if err := initDriveService(ctx); err != nil {
		log.Fatalf("FATAL: Failed to initialize Drive service: %v", err)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", webhookHandler)

	log.Printf("Server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("FATAL: failed to start server: %v", err)
	}
}