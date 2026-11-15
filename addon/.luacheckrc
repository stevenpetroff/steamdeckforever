std = "lua51"
max_line_length = 120
exclude_files = { "DeckChat/Pairing.lua" }

-- Globals the add-on defines.
globals = {
	"DeckChat_Open",
	"DeckChatDB",
	"BINDING_HEADER_DECKCHAT",
	"BINDING_NAME_DECKCHAT_OPEN",
	"SLASH_DECKCHAT1",
	"SlashCmdList",
}

-- WoW API used by the add-on.
read_globals = {
	"C_ChatInfo",
	"C_Timer",
	"ChatEdit_GetActiveWindow",
	"ChatEdit_GetLastActiveWindow",
	"ChatFrame_OpenChat",
	"ChatFrameUtil",
	"CreateFrame",
	"DEFAULT_CHAT_FRAME",
	"DeckChatPairing", -- from Pairing.lua, written by the deckchat service
	"InCombatLockdown",
	"IsAltKeyDown",
	"IsControlKeyDown",
	"IsShiftKeyDown",
	"LAST_ACTIVE_CHAT_EDIT_BOX",
	"LoggingChat",
	"NUM_CHAT_WINDOWS",
	"SetOverrideBinding",
	"UIParent",
	"UISpecialFrames",
	"strtrim",
	"tinsert",
}
