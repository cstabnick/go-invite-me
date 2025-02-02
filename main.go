package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"
	"github.com/joho/godotenv"
)

func main() {
	// Get Slack token from environment variable
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatal("Error loading .env file:", err)
	}

	slackToken := os.Getenv("SLACK_BOT_TOKEN")
	if slackToken == "" {
		log.Fatal("SLACK_TOKEN environment variable is required")
	}

	// Initialize Slack client
	slackClient := slack.New(slackToken)

	// Initialize Gin router
	r := gin.Default()

	// Initialize handler
	handler := NewGameInviteHandler(slackClient)

	// Setup routes
	r.POST("/invite", handler.SendInvite)
	r.GET("/invite", handler.GetUsageGuide)

	// Start server
	if err := r.Run(":8080"); err != nil {
		log.Fatal("Failed to start server:", err)
	}
} 