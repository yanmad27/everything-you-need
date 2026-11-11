package main

import (
	"testing"

	telebot "everything-you-need/m/services/tele-bot"
)

func TestToIncomingMessageAddressName(t *testing.T) {
	u := &telebot.User{ID: 1, Username: "nana", FirstName: "Na"}
	cases := []struct {
		from  *telebot.User
		ctype string
		want  string
	}{
		{u, "private", "Na"},
		{&telebot.User{ID: 2, Username: "bin"}, "private", "bin"},
		{u, "group", ""},
		{u, "supergroup", ""},
		{u, "", ""},
	}
	for _, c := range cases {
		m := &telebot.Message{From: c.from, Chat: telebot.Chat{ID: 5, Type: c.ctype}, Text: "pink hi"}
		if got := toIncomingMessage(m).AddressName; got != c.want {
			t.Errorf("type %q: AddressName=%q want %q", c.ctype, got, c.want)
		}
	}
}
