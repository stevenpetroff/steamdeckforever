-- DeckChat: keeps WoWChatLog.txt flushed and gives the deckchat service a
-- chord (CTRL-ALT-SHIFT-F12) that opens a clean chat box, plus a paste guard
-- chord (CTRL-ALT-SHIFT-F11) for pasting into any WoW text box.
local _, ns = ...

local CHORD = "CTRL-ALT-SHIFT-F12"
local PASTE_CHORD = "CTRL-ALT-SHIFT-F11"
-- How long Ctrl+V stays blocked after the paste guard fires.
local GUARD_SECONDS = 1
local DEFAULT_FLUSH = 2
local MIN_FLUSH, MAX_FLUSH = 1, 30

BINDING_HEADER_DECKCHAT = "DeckChat"
BINDING_NAME_DECKCHAT_OPEN = "Open a clean chat box"
BINDING_NAME_DECKCHAT_PASTE_GUARD = "Paste guard (no text box selected)"
BINDING_NAME_DECKCHAT_BLOCK = "Blocked paste (does nothing)"

local frame = CreateFrame("Frame")
-- Owns the short-lived Ctrl+V block, so clearing it leaves the chords alone.
local guardFrame = CreateFrame("Frame")
local guardPending = false
-- Bumped on every guard so only the latest guard's timer lifts the block.
local guardGeneration = 0
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
	SetOverrideBinding(frame, true, PASTE_CHORD, "DECKCHAT_PASTE_GUARD")
	if guardPending then
		ClearOverrideBindings(guardFrame)
		guardPending = false
	end
	frame:UnregisterEvent("PLAYER_REGEN_ENABLED")
end

-- Bound to DECKCHAT_PASTE_GUARD. A focused edit box swallows the chord, so
-- this only runs when no text box is selected: the phone's Ctrl+V is about
-- to arrive with nowhere to go. Block it briefly so it can't fire a keybind.
function DeckChat_PasteGuard()
	say("nothing to paste into. Click a text box in WoW, then tap Paste on your phone again.")
	if InCombatLockdown() then
		return -- bindings can't change in combat; Ctrl+V does whatever it's bound to
	end
	SetOverrideBinding(guardFrame, true, "CTRL-V", "DECKCHAT_BLOCK")
	guardPending = true
	guardGeneration = guardGeneration + 1
	local generation = guardGeneration
	C_Timer.After(GUARD_SECONDS, function()
		if generation ~= guardGeneration then
			return -- a later guard owns the block now
		end
		if InCombatLockdown() then
			frame:RegisterEvent("PLAYER_REGEN_ENABLED")
			return
		end
		ClearOverrideBindings(guardFrame)
		guardPending = false
	end)
end

function DeckChat_Block() end

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
