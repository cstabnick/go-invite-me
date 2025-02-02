package main

import (
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/slack-go/slack"
)

func main() {
	// Get Slack token from environment variable
	err := godotenv.Load(".env")
	if err != nil {
		log.Fatal("Error loading .env file:", err)
	}

	slackToken := os.Getenv("SLACK_BOT_TOKEN")
	if slackToken == "" {
		log.Fatal("SLACK_BOT_TOKEN environment variable is required")
	}

	// Initialize Slack client
	slackClient := slack.New(slackToken)

	// Initialize Gin router
	r := gin.Default()

	// Initialize handler for sending invitations via the invite API
	inviteHandler := NewGameInviteHandler(slackClient)

	// Setup routes for game invitations
	r.POST("/invite", inviteHandler.SendInvite)
	r.GET("/invite", inviteHandler.GetUsageGuide)

	// Initialize Slack Bot Handler for interactive DM flows
	slackBotHandler := NewSlackBotHandler(slackClient)
	// Setup route for receiving Slack Event callbacks
	r.POST("/slack/events", slackBotHandler.HandleEvent)

	// Start server
	if err := r.Run(":8080"); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}
