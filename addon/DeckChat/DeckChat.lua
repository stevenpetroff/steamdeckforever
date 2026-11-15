-- DeckChat: keeps WoWChatLog.txt flushed and gives the deckchat service a
-- chord (CTRL-ALT-SHIFT-F12) that opens a clean chat box.
local _, ns = ...

local CHORD = "CTRL-ALT-SHIFT-F12"
local DEFAULT_FLUSH = 2
local MIN_FLUSH, MAX_FLUSH = 1, 30

BINDING_HEADER_DECKCHAT = "DeckChat"
BINDING_NAME_DECKCHAT_OPEN = "Open a clean chat box"

local frame = CreateFrame("Frame")
local ticker

local function say(msg)
	DEFAULT_CHAT_FRAME:AddMessage("DeckChat: " .. msg)
end

local function isLogging()
	if C_ChatInfo and C_ChatInfo.IsLoggingChat then
		return C_ChatInfo.IsLoggingChat()
	end
	return LoggingChat()
end

-- WoW buffers the chat log until it is closed, so reopen it on a timer.
local function startTicker()
	if ticker then
		ticker:Cancel()
	end
	ticker = C_Timer.NewTicker(DeckChatDB.flushInterval, function()
		LoggingChat(false)
		LoggingChat(true)
	end)
end

-- Override bindings are protected, so only set them out of combat.
local function setBinding()
	if InCombatLockdown() then
		frame:RegisterEvent("PLAYER_REGEN_ENABLED")
		return
	end
	SetOverrideBinding(frame, true, CHORD, "DECKCHAT_OPEN")
	frame:UnregisterEvent("PLAYER_REGEN_ENABLED")
end

-- Newer UI code moved the ChatEdit_* globals onto ChatFrameUtil.
local function chatUtil(name, legacy)
	if ChatFrameUtil and ChatFrameUtil[name] then
		return ChatFrameUtil[name]
	end
	return legacy
end

local function activeEditBox()
	local getActive = chatUtil("GetActiveWindow", ChatEdit_GetActiveWindow)
	local getLast = chatUtil("GetLastActiveWindow", ChatEdit_GetLastActiveWindow)
	return (getActive and getActive())
		or (getLast and getLast())
		or LAST_ACTIVE_CHAT_EDIT_BOX
		or DEFAULT_CHAT_FRAME.editBox
end

-- Bound to DECKCHAT_OPEN: open the chat box empty, keeping the sticky channel.
function DeckChat_Open()
	local editBox = activeEditBox()
	if editBox then
		editBox:SetText("")
	end
	local openChat = chatUtil("OpenChat", ChatFrame_OpenChat)
	if openChat then
		openChat("")
	end
end

-- Key bindings don't fire while an edit box has focus, so the chord also
-- clears an already-open box directly.
local function onEditBoxKeyDown(self, key)
	if key == "F12" and IsControlKeyDown() and IsAltKeyDown() and IsShiftKeyDown() then
		self:SetText("")
	end
end

local function hookEditBoxes()
	for i = 1, NUM_CHAT_WINDOWS do
		local editBox = _G["ChatFrame" .. i .. "EditBox"]
		if editBox then
			editBox:HookScript("OnKeyDown", onEditBoxKeyDown)
		end
	end
end

frame:SetScript("OnEvent", function(_, event)
	if event == "PLAYER_LOGIN" then
		DeckChatDB = DeckChatDB or {}
		DeckChatDB.flushInterval = DeckChatDB.flushInterval or DEFAULT_FLUSH
		LoggingChat(true)
		startTicker()
		hookEditBoxes()
		setBinding()
	elseif event == "PLAYER_REGEN_ENABLED" then
		setBinding()
	end
end)
frame:RegisterEvent("PLAYER_LOGIN")

SLASH_DECKCHAT1 = "/deckchat"
SlashCmdList.DECKCHAT = function(input)
	local cmd, arg = strtrim(input or ""):lower():match("^(%S*)%s*(.-)$")
	if cmd == "pair" then
		ns.ShowPairFrame()
	elseif cmd == "status" then
		say(("chat logging %s, flush every %d s"):format(
			isLogging() and "on" or "OFF", DeckChatDB.flushInterval))
	elseif cmd == "flush" and tonumber(arg) then
		local seconds = math.floor(tonumber(arg))
		DeckChatDB.flushInterval = math.max(MIN_FLUSH, math.min(MAX_FLUSH, seconds))
		startTicker()
		say(("flush interval set to %d s"):format(DeckChatDB.flushInterval))
	else
		say("commands: /deckchat pair | status | flush <1-30>")
	end
end
