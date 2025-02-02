package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"
)

// SlackBotHandler processes DM events and manages a simple conversation state.
type SlackBotHandler struct {
	slackClient        *slack.Client
	conversationMutex  sync.Mutex
	conversationStates map[string]*ConversationState // keyed by the user's Slack ID
}

// ConversationState holds the current conversation
// step and data for a given user.
type ConversationState struct {
	Step             string   // possible values: "awaiting_names", "awaiting_game"
	RecipientUserIDs []string // recipients matched from the fuzzy search
}

// SlackEventCallback is a minimal struct for Slack event callbacks.
type SlackEventCallback struct {
	Token     string     `json:"token"`
	Type      string     `json:"type"`
	Challenge string     `json:"challenge,omitempty"`
	Event     SlackEvent `json:"event"`
}

// SlackEvent holds the relevant parts of the event (only message events are handled).
type SlackEvent struct {
	Type    string `json:"type"`
	User    string `json:"user"`
	Text    string `json:"text"`
	Channel string `json:"channel"`
	BotID   string `json:"bot_id,omitempty"`
}

// NewSlackBotHandler creates a new SlackBotHandler with an empty conversation state.
func NewSlackBotHandler(slackClient *slack.Client) *SlackBotHandler {
	return &SlackBotHandler{
		slackClient:        slackClient,
		conversationStates: make(map[string]*ConversationState),
	}
}

// HandleEvent is our Gin handler for Slack Events. It responds to URL verification and processes message events.
func (h *SlackBotHandler) HandleEvent(c *gin.Context) {
	var eventCallback SlackEventCallback
	if err := c.BindJSON(&eventCallback); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Handle URL verification challenge
	if eventCallback.Type == "url_verification" {
		c.JSON(http.StatusOK, gin.H{"challenge": eventCallback.Challenge})
		return
	}

	// Process message events (ignoring messages from bots)
	if eventCallback.Event.Type == "message" && eventCallback.Event.BotID == "" {
		userID := eventCallback.Event.User
		text := eventCallback.Event.Text
		channelID := eventCallback.Event.Channel

		// Lock to safely access conversation state
		h.conversationMutex.Lock()
		state, exists := h.conversationStates[userID]
		if !exists {
			// Start a new conversation – ask for the names to send to.
			state = &ConversationState{
				Step: "awaiting_names",
			}
			h.conversationStates[userID] = state
			h.conversationMutex.Unlock()

			h.sendMessage(channelID, "Hi! Who do you want to message? Please provide a comma separated list of names.")
			c.Status(http.StatusOK)
			return
		}

		// Process conversation state depending on the current step.
		if state.Step == "awaiting_names" {
			// Parse the comma separated input
			names := strings.Split(text, ",")
			var trimmedNames []string
			for _, name := range names {
				trimmedNames = append(trimmedNames, strings.TrimSpace(name))
			}

			// Get all Slack users (filtering out bots and deleted users)
			users, err := h.slackClient.GetUsers()
			if err != nil {
				h.sendMessage(channelID, "Error fetching users for matching: "+err.Error())
				h.conversationMutex.Unlock()
				c.Status(http.StatusInternalServerError)
				return
			}
			var validUsers []slack.User
			for _, u := range users {
				if !u.IsBot && !u.Deleted {
					validUsers = append(validUsers, u)
				}
			}

			// Fuzzy match (case-insensitive substring match) for each input name.
			var matchedUserIDs []string
			var matchedNames []string
			var unmatched []string

			for _, inputName := range trimmedNames {
				found := false
				for _, user := range validUsers {
					if strings.Contains(strings.ToLower(user.Name), strings.ToLower(inputName)) ||
						strings.Contains(strings.ToLower(user.RealName), strings.ToLower(inputName)) {
						matchedUserIDs = append(matchedUserIDs, user.ID)
						matchedNames = append(matchedNames, user.RealName)
						found = true
						break
					}
				}
				if !found {
					unmatched = append(unmatched, inputName)
				}
			}

			// Save the recipients and advance the conversation state.
			state.RecipientUserIDs = matchedUserIDs
			state.Step = "awaiting_game"
			h.conversationMutex.Unlock()

			reply := ""
			if len(matchedNames) > 0 {
				reply += "Matched recipients: " + strings.Join(matchedNames, ", ") + ".\n"
			}
			if len(unmatched) > 0 {
				reply += "Could not match: " + strings.Join(unmatched, ", ") + ".\n"
			}
			reply += "Please enter the name of the game you'd like to play."
			h.sendMessage(channelID, reply)
			c.Status(http.StatusOK)
			return
		} else if state.Step == "awaiting_game" {
			// The user has replied with the game name.
			gameName := text
			h.conversationMutex.Unlock()

			// Use OpenAI's API to generate an invitation message.
			invitationMessage, err := callOpenAIGPT(gameName)
			if err != nil {
				h.sendMessage(channelID, "Error contacting GPT: "+err.Error())
				h.deleteConversation(userID)
				c.Status(http.StatusInternalServerError)
				return
			}

			// Forward the invitation to all recipients.
			h.conversationMutex.Lock()
			recipientIDs := state.RecipientUserIDs
			h.conversationMutex.Unlock()

			var sendErrors []string
			for _, rid := range recipientIDs {
				_, _, err = h.slackClient.PostMessage(
					rid,
					slack.MsgOptionText(invitationMessage, false),
				)
				if err != nil {
					sendErrors = append(sendErrors, err.Error())
				}
			}
			if len(sendErrors) > 0 {
				h.sendMessage(channelID, "Failed to send invitations to some recipients: "+strings.Join(sendErrors, "; "))
			} else {
				h.sendMessage(channelID, "Invitation sent successfully!")
			}
			h.deleteConversation(userID)
			c.Status(http.StatusOK)
			return
		}
		h.conversationMutex.Unlock()
	}
	c.Status(http.StatusOK)
}

// sendMessage is a helper to send a plain-text message to a given channel.
func (h *SlackBotHandler) sendMessage(channel, text string) {
	_, _, err := h.slackClient.PostMessage(
		channel,
		slack.MsgOptionText(text, false),
	)
	if err != nil {
		log.Println("Failed to send message to channel", channel, ":", err)
	}
}

// deleteConversation removes a user's conversation state.
func (h *SlackBotHandler) deleteConversation(userID string) {
	h.conversationMutex.Lock()
	delete(h.conversationStates, userID)
	h.conversationMutex.Unlock()
}

// callOpenAIGPT pings the OpenAI API with a prompt to generate a game invitation.
// It expects the OPENAI_API_KEY environment variable to be set.
func callOpenAIGPT(gameName string) (string, error) {
	openaiAPIKey := os.Getenv("OPENAI_API_KEY")
	if openaiAPIKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY not set")
	}
	url := "https://api.openai.com/v1/chat/completions"
	requestBody := map[string]interface{}{
		"model": "gpt-3.5-turbo",
		"messages": []map[string]string{
			{"role": "user", "content": fmt.Sprintf("You are a game invitation generator. Write a friendly invitation for playing the game %s.", gameName)},
		},
		"max_tokens": 50,
	}
	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+openaiAPIKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI API error: %s", string(bodyBytes))
	}

	var responseData struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&responseData); err != nil {
		return "", err
	}
	if len(responseData.Choices) == 0 {
		return "", fmt.Errorf("No response from OpenAI")
	}
	return responseData.Choices[0].Message.Content, nil
} 