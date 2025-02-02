package main

import (
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/slack-go/slack"
)

// SlackBotHandler processes app_mention and direct message events and manages a simple conversation state.
type SlackBotHandler struct {
	slackClient        *slack.Client
	conversationMutex  sync.Mutex
	conversationStates map[string]*ConversationState // keyed by the user's Slack ID
}

// ConversationState holds the current conversation step and data for a given user.
type ConversationState struct {
	Step             string   // possible values: "awaiting_names", "awaiting_question"
	RecipientUserIDs []string // recipients matched from the fuzzy search
}

// SlackEventCallback is a minimal struct for Slack event callbacks.
type SlackEventCallback struct {
	Token     string     `json:"token"`
	Type      string     `json:"type"`
	Challenge string     `json:"challenge,omitempty"`
	Event     SlackEvent `json:"event"`
}

// SlackEvent holds the relevant parts of the event (we handle both app_mention and direct message events).
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

// HandleEvent is our Gin handler for Slack events.
// It responds to URL verification and processes both app_mention events and direct messages.
func (h *SlackBotHandler) HandleEvent(c *gin.Context) {
	var eventCallback SlackEventCallback
	if err := c.BindJSON(&eventCallback); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Log incoming event details.
	log.Printf("Received Slack event: Type=%s, User=%s, Channel=%s, Text=%s",
		eventCallback.Event.Type, eventCallback.Event.User, eventCallback.Event.Channel, eventCallback.Event.Text)

	// Handle URL verification challenge.
	if eventCallback.Type == "url_verification" {
		log.Println("Handling URL verification challenge")
		c.JSON(http.StatusOK, gin.H{"challenge": eventCallback.Challenge})
		return
	}

	channelID := eventCallback.Event.Channel
	isDirectMessage := strings.HasPrefix(channelID, "D")
	isAppMention := eventCallback.Event.Type == "app_mention"

	// Process event if it's an app mention or a direct message (ignoring bot messages)
	if (isAppMention || isDirectMessage) && eventCallback.Event.BotID == "" {
		userID := eventCallback.Event.User
		
		// Use different text processing based on event type.
		var text string
		if isAppMention {
			text = removeBotMention(eventCallback.Event.Text)
		} else {
			text = eventCallback.Event.Text
		}
		log.Printf("Processed text from user %s: %s", userID, text)

		h.conversationMutex.Lock()
		state, exists := h.conversationStates[userID]
		if !exists {
			// Start a new conversation – ask for the names to send to.
			log.Printf("No conversation state for user %s, starting new conversation.", userID)
			state = &ConversationState{
				Step: "awaiting_names",
			}
			h.conversationStates[userID] = state
			h.conversationMutex.Unlock()

			log.Printf("Sent greeting to user %s asking for recipient names.", userID)
			h.sendMessage(channelID, "Hi! Who do you want to message? Please provide a comma separated list of names.")
			c.Status(http.StatusOK)
			return
		}

		// Process conversation state depending on the current step.
		if state.Step == "awaiting_names" {
			log.Printf("User %s is in state 'awaiting_names'. Input text: %s", userID, text)
			// Parse the comma separated input.
			names := strings.Split(text, ",")
			var trimmedNames []string
			for _, name := range names {
				trimmedNames = append(trimmedNames, strings.TrimSpace(name))
			}
			log.Printf("Parsed names for user %s: %v", userID, trimmedNames)

			// Fetch all Slack users (filtering out bots and deleted accounts).
			users, err := h.slackClient.GetUsers()
			if err != nil {
				log.Printf("Error fetching users for matching: %v", err)
				h.sendMessage(channelID, "Error fetching users for matching: "+err.Error())
				h.conversationMutex.Unlock()
				c.Status(http.StatusInternalServerError)
				return
			}
			var validUsers []slack.User
			var allValidNames []string
			for _, u := range users {
				if !u.IsBot && !u.Deleted {
					validUsers = append(validUsers, u)
					allValidNames = append(allValidNames, u.RealName)
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
						log.Printf("Matched input '%s' to user '%s' (ID: %s)", inputName, user.RealName, user.ID)
						matchedUserIDs = append(matchedUserIDs, user.ID)
						matchedNames = append(matchedNames, user.RealName)
						found = true
						break
					}
				}
				if !found {
					log.Printf("No match found for input '%s'", inputName)
					unmatched = append(unmatched, inputName)
				}
			}

			// If any names did not match, respond with details of errors and the list of all possible valid user names,
			// and remain in the same state.
			if len(unmatched) > 0 {
				reply := "Could not match the following names: " + strings.Join(unmatched, ", ") + ".\n"
				reply += "Valid user names include: " + strings.Join(allValidNames, ", ") + ".\n"
				reply += "Please provide a correct comma separated list of names."
				h.conversationMutex.Unlock()
				log.Printf("Unmatched names for user %s: %v", userID, unmatched)
				h.sendMessage(channelID, reply)
				c.Status(http.StatusOK)
				return
			}

			// Otherwise, update state and advance to the question prompt.
			state.RecipientUserIDs = matchedUserIDs
			state.Step = "awaiting_question"
			h.conversationMutex.Unlock()

			reply := "Matched recipients: " + strings.Join(matchedNames, ", ") + ".\n"
			reply += "What do you want to ask?"
			log.Printf("Advancing conversation state to 'awaiting_question' for user %s", userID)
			h.sendMessage(channelID, reply)
			c.Status(http.StatusOK)
			return
		} else if state.Step == "awaiting_question" {
			log.Printf("User %s is in state 'awaiting_question'. Received question: %s", userID, text)
			question := text
			h.conversationMutex.Unlock()

			// Forward the question to all matched recipients.
			log.Printf("Forwarding question from user %s to recipients: %v", userID, state.RecipientUserIDs)
			var sendErrors []string
			for _, rid := range state.RecipientUserIDs {
				_, _, err := h.slackClient.PostMessage(
					rid,
					slack.MsgOptionText(question, false),
				)
				if err != nil {
					log.Printf("Error sending message to recipient %s: %v", rid, err)
					sendErrors = append(sendErrors, err.Error())
				} else {
					log.Printf("Successfully sent question to recipient %s", rid)
				}
			}
			if len(sendErrors) > 0 {
				h.sendMessage(channelID, "Failed to send question to some recipients: "+strings.Join(sendErrors, "; "))
			} else {
				h.sendMessage(channelID, "Your question was sent successfully!")
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
	log.Printf("Sending message to channel %s: %s", channel, text)
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
	log.Printf("Deleting conversation state for user %s", userID)
	h.conversationMutex.Lock()
	delete(h.conversationStates, userID)
	h.conversationMutex.Unlock()
}

// removeBotMention removes the first mention (typically @AppName) from the given text.
func removeBotMention(text string) string {
	if strings.HasPrefix(text, "<@") {
		if end := strings.Index(text, ">"); end != -1 {
			return strings.TrimSpace(text[end+1:])
		}
	}
	return text
} 