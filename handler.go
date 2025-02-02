package main

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"
)

type GameInviteHandler struct {
	slackClient *slack.Client
}

type InviteRequest struct {
	GameName    string   `json:"game_name" binding:"required"`
	UserIDs     []string `json:"user_ids" binding:"required"`
	Description string   `json:"description"`
}

type UsageGuide struct {
	Description string         `json:"description"`
	Endpoints   []EndpointInfo `json:"endpoints"`
	UserIDs     []UserInfo     `json:"users"`
}

type EndpointInfo struct {
	Path        string `json:"path"`
	Method      string `json:"method"`
	Description string `json:"description"`
	Example     any    `json:"example,omitempty"`
}

type UserInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	RealName string `json:"real_name"`
}

func NewGameInviteHandler(slackClient *slack.Client) *GameInviteHandler {
	return &GameInviteHandler{
		slackClient: slackClient,
	}
}

func (h *GameInviteHandler) SendInvite(c *gin.Context) {
	var req InviteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Create a message with blocks for better formatting
	blocks := []slack.Block{
		slack.NewHeaderBlock(
			slack.NewTextBlockObject("plain_text", "Game Invitation: "+req.GameName, false, false),
		),
		slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", req.Description, false, false),
			nil,
			nil,
		),
		slack.NewActionBlock(
			"game_actions",
			slack.NewButtonBlockElement(
				"accept_game",
				"accept",
				slack.NewTextBlockObject("plain_text", "Accept", false, false),
			).WithStyle(slack.StylePrimary),
			slack.NewButtonBlockElement(
				"decline_game",
				"decline",
				slack.NewTextBlockObject("plain_text", "Decline", false, false),
			).WithStyle(slack.StyleDanger),
		),
	}

	// Create channels for error handling
	errChan := make(chan error, len(req.UserIDs))
	var wg sync.WaitGroup

	// Send messages concurrently
	for _, userID := range req.UserIDs {
		wg.Add(1)
		go func(uid string) {
			defer wg.Done()
			_, _, err := h.slackClient.PostMessage(
				uid,
				slack.MsgOptionBlocks(blocks...),
				slack.MsgOptionText("Game Invitation: "+req.GameName, false),
			)
			if err != nil {
				errChan <- fmt.Errorf("failed to send invitation to user %s: %w", uid, err)
			}
		}(userID)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Check for any errors
	var errors []string
	for err := range errChan {
		errors = append(errors, err.Error())
	}

	if len(errors) > 0 {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to send some invitations",
			"details": errors,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Invitations sent successfully"})
}

func (h *GameInviteHandler) GetUsageGuide(c *gin.Context) {
	// Fetch users from Slack
	users, err := h.slackClient.GetUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch users: " + err.Error()})
		return
	}

	// Convert slack users to our UserInfo format
	userInfos := make([]UserInfo, 0, len(users))
	for _, user := range users {
		if !user.IsBot && !user.Deleted {
			userInfos = append(userInfos, UserInfo{
				ID:       user.ID,
				Name:     user.Name,
				RealName: user.RealName,
			})
		}
	}

	// Create usage guide
	guide := UsageGuide{
		Description: "API for sending game invitations via Slack",
		Endpoints: []EndpointInfo{
			{
				Path:        "/invite",
				Method:      "POST",
				Description: "Send game invitations to specified users",
				Example: InviteRequest{
					GameName:    "Chess",
					UserIDs:     []string{"U0123456", "U6543210"},
					Description: "Want to play a quick game of chess?",
				},
			},
			{
				Path:        "/invite",
				Method:      "GET",
				Description: "Get usage guide and available user IDs",
			},
		},
		UserIDs: userInfos,
	}

	c.JSON(http.StatusOK, guide)
}
