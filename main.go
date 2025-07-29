package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	bot, err := tgbotapi.NewBotAPI(os.Getenv("TELEGRAM_BOT_TOKEN"))
	if err != nil {
		log.Fatalf("Failed to create bot: %v", err)
	}

	// Cloud Run 需要監聽 PORT 環境變數指定的端口
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var update tgbotapi.Update
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			log.Printf("could not decode incoming update: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if update.Message == nil {
			// 忽略非訊息的更新
			w.WriteHeader(http.StatusOK)
			return
		}

		log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

		// 建立一個回覆訊息
		msg := tgbotapi.NewMessage(update.Message.Chat.ID, update.Message.Text)
		msg.ReplyToMessageID = update.Message.MessageID

		// 發送訊息
		if _, err := bot.Send(msg); err != nil {
			log.Printf("could not send message: %v", err)
		}

		w.WriteHeader(http.StatusOK)
	})

	log.Printf("starting server on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}
