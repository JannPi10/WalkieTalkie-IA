package models

import (
	"testing"
)

func TestUser_IsInChannel(t *testing.T) {
	tests := []struct {
		name     string
		user     User
		expected bool
	}{
		{
			name: "user with channel",
			user: User{
				CurrentChannelID: &[]uint{1}[0],
			},
			expected: true,
		},
		{
			name: "user without channel",
			user: User{
				CurrentChannelID: nil,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.user.IsInChannel()
			if result != tt.expected {
				t.Errorf("IsInChannel() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestUser_GetCurrentChannelCode(t *testing.T) {
	channel := Channel{
		Code: "canal-1",
	}

	tests := []struct {
		name     string
		user     User
		expected string
	}{
		{
			name: "user with current channel",
			user: User{
				CurrentChannel: &channel,
			},
			expected: "canal-1",
		},
		{
			name: "user without current channel",
			user: User{
				CurrentChannel: nil,
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.user.GetCurrentChannelCode()
			if result != tt.expected {
				t.Errorf("GetCurrentChannelCode() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
