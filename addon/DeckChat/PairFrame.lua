-- Draws the pairing QR code from DeckChatPairing (written by the deckchat
-- service into Pairing.lua) using plain colour textures.
local _, ns = ...

local MODULE = 5 -- pixels per QR module
local QUIET = 4  -- quiet-zone width in modules
local PAD = 12   -- frame padding around the white QR area

local frame

local function validPairing(p)
	return type(p) == "table" and type(p.url) == "string"
		and type(p.size) == "number" and type(p.rows) == "table"
		and #p.rows == p.size
end

local function square(parent, layer, x, y, w, h, r, g, b)
	local tex = parent:CreateTexture(nil, layer)
	tex:SetColorTexture(r, g, b, 1)
	tex:SetPoint("TOPLEFT", x, -y)
	tex:SetSize(w, h)
	return tex
end

local function build(p)
	local qr = (p.size + 2 * QUIET) * MODULE
	local f = CreateFrame("Frame", "DeckChatPairFrame", UIParent)
	f:SetSize(qr + 2 * PAD, qr + 2 * PAD + 40)
	f:SetPoint("CENTER")
	f:SetFrameStrata("DIALOG")
	f:SetMovable(true)
	f:EnableMouse(true)
	f:RegisterForDrag("LeftButton")
	f:SetScript("OnDragStart", f.StartMoving)
	f:SetScript("OnDragStop", f.StopMovingOrSizing)
	f:SetClampedToScreen(true)
	tinsert(UISpecialFrames, "DeckChatPairFrame") -- Escape closes it

	local bg = f:CreateTexture(nil, "BACKGROUND")
	bg:SetAllPoints()
	bg:SetColorTexture(0, 0, 0, 0.85)

	-- White quiet zone, then one black texture per horizontal run of dark modules.
	square(f, "BORDER", PAD, PAD, qr, qr, 1, 1, 1)
	local origin = PAD + QUIET * MODULE
	for y, row in ipairs(p.rows) do
		local x = 1
		while x <= #row do
			local s, e = row:find("1+", x)
			if not s then
				break
			end
			square(f, "ARTWORK", origin + (s - 1) * MODULE, origin + (y - 1) * MODULE,
				(e - s + 1) * MODULE, MODULE, 0, 0, 0)
			x = e + 1
		end
	end

	local url = f:CreateFontString(nil, "OVERLAY", "GameFontHighlightSmall")
	url:SetPoint("TOP", 0, -(PAD + qr + 8))
	url:SetWidth(qr)
	url:SetText(p.url)

	local close = CreateFrame("Button", nil, f, "UIPanelCloseButton")
	close:SetPoint("TOPRIGHT", 2, 2)
	return f
end

function ns.ShowPairFrame()
	if not validPairing(DeckChatPairing) then
		DEFAULT_CHAT_FRAME:AddMessage(
			"DeckChat: start the deckchat service on your Deck, then restart WoW.")
		return
	end
	frame = frame or build(DeckChatPairing)
	frame:Show()
end
