package mtg

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	scryfall "github.com/BlueMonday/go-scryfall"
	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"

	"github.com/jeandeaual/tts-deckconverter/log"
	"github.com/jeandeaual/tts-deckconverter/plugins"
)

const (
	defaultBackURL    = "http://cloud-3.steamusercontent.com/ugc/998016607072060763/7AFEF2CE9E7A7DB735C93CF33CC4C378CBF4B20D/"
	planechaseBackURL = "http://cloud-3.steamusercontent.com/ugc/998016607072060000/1713AE8643632456D06F1BBA962C5514DD8CCC76/"
	archenemyBackURL  = "http://cloud-3.steamusercontent.com/ugc/998016607072055936/0598975AB8EC26E8956D84F9EC73BBE5754E6C80/"
	// M filler card back
	// See http://www.magiclibrarities.net/348-rarities-filler-cards-english-cards-fillers.html
	mFillerBackURL  = "http://cloud-3.steamusercontent.com/ugc/998016607072059554/6BF846C387B045FF524AE42758F6962FE3774CDB/"
	apiCallInterval = 100 * time.Millisecond
)

var cardLineRegexps = []*regexp.Regexp{
	// Magic Arena format
	regexp.MustCompile(`^\s*(?P<Count>\d+)\s+(?P<Name>.+)\s+\((?P<Set>[A-Z0-9_]+)\)(\s+(?P<NumberInSet>[\d]+[ab]*))?$`),
	// Magic Workstation format
	regexp.MustCompile(`^(?P<Sideboard>SB:)?\s*(?P<Count>\d+)\s+\[(?P<Set>[A-Z0-9_]+)\]\s+(?P<Name>.+)$`),
	// Standard format (MTGO, etc.)
	regexp.MustCompile(`^(?P<Sideboard>SB:)?\s*(?P<Count>\d+)x?\s+(?P<Name>[^#]+)(\s+#(?P<Comment>.*))?$`),
	// TODO: MTG Salvation
	// https://github.com/Yomguithereal/mtgparser
}

// DeckType is the type of a parsed deck.
type DeckType int

const (
	// Main deck
	Main DeckType = iota
	// Sideboard deck
	Sideboard
	// Maybeboard cards
	Maybeboard
)

// CardInfo contains the name of a card and its set.
type CardInfo struct {
	// Name of the card.
	Name string
	// Set of the card.
	Set *string
}

// CardNames contains the card names and their count.
type CardNames struct {
	// Names are the card names.
	Names []CardInfo
	// Counts is a map of card name to count (number of this card in the deck).
	Counts map[string]int
}

// NewCardNames creates a new CardNames struct.
func NewCardNames() *CardNames {
	counts := make(map[string]int)
	return &CardNames{Counts: counts}
}

// Insert a new card in a CardNames struct.
func (c *CardNames) Insert(name string, set *string) {
	c.InsertCount(name, set, 1)
}

// InsertCount inserts several new cards in a CardNames struct.
func (c *CardNames) InsertCount(name string, set *string, count int) {
	_, found := c.Counts[name]
	if !found {
		c.Names = append(c.Names, CardInfo{
			Name: name,
			Set:  set,
		})
		c.Counts[name] = count
	} else {
		c.Counts[name] = c.Counts[name] + count
	}
}

// String representation of a CardNames struct.
func (c *CardNames) String() string {
	var sb strings.Builder

	for _, cardInfo := range c.Names {
		count := c.Counts[cardInfo.Name]
		sb.WriteString(strconv.Itoa(count))
		sb.WriteString(" ")
		sb.WriteString(cardInfo.Name)
		sb.WriteString("\n")
	}

	return sb.String()
}

func getImageURL(
	uris *scryfall.ImageURIs,
	highResAvailable bool,
	imageQuality string,
) string {
	var imageURL string

	switch imageQuality {
	case string(small):
		imageURL = uris.Small
	case string(normal):
		imageURL = uris.Normal
	case string(large):
		if highResAvailable {
			imageURL = uris.Large
		} else {
			log.Warn("High-resolution image not available, using normal quality instead of large")
			imageURL = uris.Normal
		}
	case string(png):
		if highResAvailable {
			imageURL = uris.PNG
		} else {
			log.Warn("High-resolution image not available, using normal quality instead of png")
			imageURL = uris.Normal
		}
	}

	return imageURL
}

func cardNamesToDeck(cards *CardNames, name string, options map[string]interface{}) (*plugins.Deck, error) {
	ctx := context.Background()
	deck := &plugins.Deck{
		Name:     name,
		BackURL:  MagicPlugin.AvailableBacks()[plugins.DefaultBackKey].URL,
		CardSize: plugins.CardSizeStandard,
	}
	client, err := scryfall.NewClient()
	if err != nil {
		log.Error(err)
	}

	imageQuality := MagicPlugin.AvailableOptions()["quality"].DefaultValue.(string)
	if quality, found := options["quality"]; found {
		imageQuality = quality.(string)
	}

	for _, cardInfo := range cards.Names {
		count := cards.Counts[cardInfo.Name]

		opts := scryfall.GetCardByNameOptions{}
		if cardInfo.Set != nil {
			opts.Set = *cardInfo.Set
		}
		// Fuzzy search is required to match card names in languages other
		// than English ("printed_name")
		card, err := client.GetCardByName(ctx, cardInfo.Name, false, opts)
		if err != nil {
			log.Errorw(
				"Scryfall client error",
				"error", err,
				"name", cardInfo.Name,
				"options", opts,
			)
			return deck, err
		}

		log.Debugf("API response: %v", card)

		var rulings []scryfall.Ruling

		// Check the options to see if we want the rulings
		if showRulings, found := options["show_rulings"]; found && showRulings.(bool) {
			time.Sleep(apiCallInterval)
			rulings, err = client.GetRulings(ctx, card.ID)
			if err != nil {
				log.Errorw(
					"Scryfall client error",
					"error", err,
					"name", cardInfo.Name,
					"options", opts,
				)
				return deck, err
			}
		}

		if card.Layout == scryfall.LayoutMeld {
			// Meld card
			// Find the URL of the meld_result
			if len(card.AllParts) == 0 {
				log.Errorf("No meld parts found for card %s", card.Name)
				continue
			}
			var meldResultURI string
			for _, part := range card.AllParts {
				if part.Component == scryfall.ComponentMeldResult {
					meldResultURI = part.URI
					break
				}
			}
			if len(meldResultURI) == 0 {
				log.Errorf("No meld result found for card %s", card.Name)
				continue
			}
			uriParts := strings.Split(meldResultURI, "/")
			meldResultID := uriParts[len(uriParts)-1]

			log.Debugf("Querying meld result (card ID %s)", meldResultID)

			meldResult, err := client.GetCard(ctx, meldResultID)
			if err != nil {
				log.Errorw(
					"Scryfall client error",
					"error", err,
					"id", meldResultID,
				)
				continue
			}

			imageURL := getImageURL(card.ImageURIs, card.HighresImage, imageQuality)
			meldResultImageURL := getImageURL(meldResult.ImageURIs, meldResult.HighresImage, imageQuality)

			deck.Cards = append(deck.Cards, plugins.CardInfo{
				Name:        card.Name,
				Description: buildCardDescription(card, rulings),
				ImageURL:    imageURL,
				Count:       count,
				AlternativeState: &plugins.CardInfo{
					Name:        meldResult.Name,
					Description: buildCardDescription(meldResult, rulings),
					ImageURL:    meldResultImageURL,
					Oversized:   true,
				},
			})
		} else if len(card.CardFaces) == 0 ||
			card.Layout == scryfall.LayoutFlip ||
			card.Layout == scryfall.LayoutSplit ||
			card.Layout == scryfall.LayoutAdventure {
			// Card with a single face
			if card.ImageURIs == nil {
				return deck, errors.New("no image found for card " + card.Name)
			}

			var description string

			if len(card.CardFaces) > 1 {
				// For flip, split and adventure layouts
				description = buildCardFacesDescription(card.CardFaces, rulings)
			} else {
				// For standard cards
				description = buildCardDescription(card, rulings)
			}

			imageURL := getImageURL(card.ImageURIs, card.HighresImage, imageQuality)

			deck.Cards = append(deck.Cards, plugins.CardInfo{
				Name:        card.Name,
				Description: description,
				ImageURL:    imageURL,
				Count:       count,
				Oversized:   card.Oversized,
			})
		} else {
			// For transform cards
			front := card.CardFaces[0]
			back := card.CardFaces[1]

			frontImageURL := getImageURL(&front.ImageURIs, card.HighresImage, imageQuality)
			backImageURL := getImageURL(&back.ImageURIs, card.HighresImage, imageQuality)

			deck.Cards = append(deck.Cards, plugins.CardInfo{
				Name:        front.Name,
				Description: buildCardFaceDescription(front, rulings),
				ImageURL:    frontImageURL,
				Count:       count,
				AlternativeState: &plugins.CardInfo{
					Name:        back.Name,
					Description: buildCardFaceDescription(back, rulings),
					ImageURL:    backImageURL,
				},
			})
		}

		log.Infof("Retrieved %s", cardInfo.Name)

		time.Sleep(apiCallInterval)
	}

	return deck, nil
}

func parseFile(path string, options map[string]string) ([]*plugins.Deck, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, err
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		err := file.Close()
		if err != nil {
			log.Error(err)
		}
	}()

	ext := filepath.Ext(path)
	name := strings.TrimSuffix(filepath.Base(path), ext)

	log.Debugf("Base file name: %s", name)

	return fromDeckFile(file, name, options)
}

func fromDeckFile(file io.Reader, name string, options map[string]string) ([]*plugins.Deck, error) {
	// Check the options
	validatedOptions, err := MagicPlugin.AvailableOptions().ValidateNormalize(options)
	if err != nil {
		return nil, err
	}

	main, side, maybe, err := parseDeckFile(file)
	if err != nil {
		return nil, err
	}

	var decks []*plugins.Deck

	if main != nil {
		mainDeck, err := cardNamesToDeck(main, name, validatedOptions)
		if err != nil {
			return nil, err
		}

		decks = append(decks, mainDeck)
	}

	if side != nil {
		sideDeck, err := cardNamesToDeck(side, name+" - Sideboard", validatedOptions)
		if err != nil {
			return nil, err
		}

		decks = append(decks, sideDeck)
	}

	if maybe != nil {
		maybeDeck, err := cardNamesToDeck(side, name+" - Maybeboard", validatedOptions)
		if err != nil {
			return nil, err
		}

		decks = append(decks, maybeDeck)
	}

	return decks, nil
}

func parseDeckLine(
	line string,
	main *CardNames,
	side *CardNames,
	maybe *CardNames,
	step DeckType,
	sbLineFound bool,
	emptyLineCount int,
) (
	*CardNames,
	*CardNames,
	*CardNames,
	DeckType,
	bool,
	int,
) {
	// Try to parse the line
	for _, regex := range cardLineRegexps {
		matches := regex.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		groupNames := regex.SubexpNames()
		countIdx := plugins.IndexOf("Count", groupNames)
		if countIdx == -1 {
			log.Errorf("Count not present in regex: %s", regex)
			continue
		}
		nameIdx := plugins.IndexOf("Name", groupNames)
		if nameIdx == -1 {
			log.Errorf("Name not present in regex: %s", regex)
			continue
		}
		sideboardIdx := plugins.IndexOf("Sideboard", groupNames)
		if sideboardIdx != -1 && len(matches[sideboardIdx]) > 0 && !sbLineFound {
			step = Sideboard
			log.Debug("Switched to sideboard (found line starting with \"SB:\")")

			if side != nil && len(side.Names) > 0 {
				// This is the first line starting with SB:, but we
				// already have cards in the sideboard
				// That means we found an empty line beforehand,
				// assuming this would be the sideboard separator
				main.Names = append(main.Names, side.Names...)
				for name, count := range side.Counts {
					if originalCount, found := main.Counts[name]; found {
						main.Counts[name] = originalCount + count
					} else {
						main.Counts[name] = count
					}
				}
				side = nil
			}

			sbLineFound = true
		}
		var set *string
		setIdx := plugins.IndexOf("Set", groupNames)
		if setIdx != -1 && len(matches[setIdx]) > 0 {
			if matches[setIdx] == "000" {
				// TappedOut sometimes exports decks with an invalid set
				// number ("000")
				// Ignore it
				log.Debugf("Ignoring set ID %s", matches[setIdx])
			} else {
				set = &matches[setIdx]
			}
		}

		count, err := strconv.Atoi(matches[countIdx])
		if err != nil {
			log.Errorf("Error when parsing count: %s", err)
			continue
		}
		name := strings.TrimSpace(matches[nameIdx])

		// Some formats use 3 slashes for split cards
		// Since Scryfall uses 2 slashes, replace them
		if strings.Contains(name, "///") {
			name = strings.Replace(name, "///", "//", 1)
		}

		log.Debugw(
			"Found card",
			"name", name,
			"count", count,
			"step", step,
			"regex", regex,
			"matches", matches,
			"groupNames", groupNames,
		)

		if step == Main {
			if main == nil {
				main = NewCardNames()
			}
			main.InsertCount(name, set, count)
		} else if step == Sideboard {
			if side == nil {
				side = NewCardNames()
			}
			side.InsertCount(name, set, count)
		} else if step == Maybeboard {
			if maybe == nil {
				maybe = NewCardNames()
			}
			maybe.InsertCount(name, set, count)
		} else {
			log.Errorw(
				"Found card info but deck not specified",
				"line", line,
			)
		}

		break
	}

	return main, side, maybe, step, sbLineFound, emptyLineCount
}

func parseDeckFile(file io.Reader) (*CardNames, *CardNames, *CardNames, error) {
	var (
		main  *CardNames
		side  *CardNames
		maybe *CardNames
	)
	step := Main
	scanner := bufio.NewScanner(file)
	sbLineFound := false
	emptyLineCount := 0

	for scanner.Scan() {
		line := scanner.Text()

		if len(line) == 0 {
			// Empty line
			// If we already found a main deck card, this empty line means
			// we switched to the sideboard
			if main != nil && len(main.Names) > 0 {
				if step == Main {
					step = Sideboard
					log.Debug("Switched to sideboard (found empty line)")
				}
				emptyLineCount++
			}
			continue
		}

		if strings.HasPrefix(line, "Sideboard") {
			if step == Main {
				step = Sideboard
				log.Debug("Switched to sideboard (found comment)")
			}
			continue
		}

		if strings.HasPrefix(line, "Maybeboard") {
			step = Maybeboard
			log.Debug("Switched to maybeboard (found comment)")
			continue
		}

		if strings.HasPrefix(line, "//") {
			// Comment, ignore
			continue
		}

		main, side, maybe, step, sbLineFound, emptyLineCount = parseDeckLine(
			line,
			main,
			side,
			maybe,
			step,
			sbLineFound,
			emptyLineCount,
		)
	}

	if side != nil && !sbLineFound && emptyLineCount > 1 {
		// Multiple empty lines with no line starting with "SB:", that means
		// there was no sideboard
		main.Names = append(main.Names, side.Names...)
		for name, count := range side.Counts {
			if originalCount, found := main.Counts[name]; found {
				main.Counts[name] = originalCount + count
			} else {
				main.Counts[name] = count
			}
		}
		side = nil
	}

	if main != nil {
		log.Debugf("Main: %d different card(s)\n%v", len(main.Names), main)
	} else {
		log.Debug("Main: 0 cards")
	}
	if side != nil {
		log.Debugf("Sideboard: %d different card(s)\n%v", len(side.Names), side)
	} else {
		log.Debug("Sideboard: 0 cards")
	}
	if maybe != nil {
		log.Debugf("Maybeboard: %d different card(s)\n%v", len(maybe.Names), side)
	} else {
		log.Debug("Maybeboard: 0 cards")
	}

	if err := scanner.Err(); err != nil {
		log.Error(err)
		return main, side, maybe, err
	}

	return main, side, maybe, nil
}

func queryDeckFile(fileURL string, deckName string, options map[string]string) (decks []*plugins.Deck, err error) {
	// Build the request
	req, err := http.NewRequest("GET", fileURL, nil)
	if err != nil {
		return nil, fmt.Errorf("couldn't create request for %s: %w", fileURL, err)
	}

	client := &http.Client{}

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't query %s: %w", fileURL, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("couldn't close the response body: %w", cerr)
		}
	}()

	return fromDeckFile(resp.Body, deckName, options)
}

func handleLink(url, titleXPath, fileURL string, options map[string]string) (decks []*plugins.Deck, err error) {
	log.Infof("Checking %s", url)
	doc, err := htmlquery.LoadURL(url)
	if err != nil {
		return nil, fmt.Errorf("couldn't query %s: %w", url, err)
	}

	// Find the title
	title := htmlquery.FindOne(doc, titleXPath)
	if title == nil {
		return nil, fmt.Errorf("no title found in %s (XPath: %s)", url, titleXPath)
	}
	deckName := strings.TrimSpace(htmlquery.InnerText(title))
	log.Infof("Found title: %s", deckName)

	return queryDeckFile(fileURL, deckName, options)
}

// deckbox.org exports it's decks in HTML for some reason
func handleHTMLLink(url, titleXPath, fileURL string, options map[string]string) ([]*plugins.Deck, error) {
	log.Infof("Checking %s", url)
	doc, err := htmlquery.LoadURL(url)
	if err != nil {
		return nil, fmt.Errorf("couldn't query %s: %w", url, err)
	}

	// Find the title
	title := htmlquery.FindOne(doc, titleXPath)
	if title == nil {
		return nil, fmt.Errorf("no title found in %s (XPath: %s)", fileURL, titleXPath)
	}
	name := strings.TrimSpace(htmlquery.InnerText(title))
	log.Infof("Found title: %s", name)

	// Retrieve the file
	htmlFile, err := htmlquery.LoadURL(fileURL)
	if err != nil {
		return nil, fmt.Errorf("couldn't query %s: %w", fileURL, err)
	}
	body := htmlquery.FindOne(htmlFile, `//body`)
	if body == nil {
		return nil, fmt.Errorf("no body found in %s", fileURL)
	}

	var output func(buf *bytes.Buffer, n *html.Node)
	output = func(buf *bytes.Buffer, n *html.Node) {
		switch n.Type {
		case html.TextNode:
			buf.WriteString(strings.TrimSpace(n.Data))
			return
		case html.ElementNode:
			// Convert <br> and <br/> to newlines
			if n.Data == "br" {
				buf.WriteString("\n")
				return
			}
			if n.Data == "p" {
				buf.WriteString("\n")
			}
		case html.CommentNode:
			return
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			output(buf, child)
		}
		if n.Type == html.ElementNode && n.Data == "p" {
			buf.WriteString("\n")
		}
	}

	var buffer bytes.Buffer
	output(&buffer, body)

	log.Debug("Retrieved deck: " + buffer.String())

	return fromDeckFile(bytes.NewReader(buffer.Bytes()), name, options)
}

func handleLinkWithDownloadLink(url, titleXPath, fileXPath, baseURL string, options map[string]string) (decks []*plugins.Deck, err error) {
	log.Infof("Checking %s", url)
	doc, err := htmlquery.LoadURL(url)
	if err != nil {
		return nil, fmt.Errorf("couldn't query %s: %w", url, err)
	}

	// Find the title
	title := htmlquery.FindOne(doc, titleXPath)
	if title == nil {
		return nil, fmt.Errorf("no title found in %s (XPath: %s)", url, titleXPath)
	}
	deckName := strings.TrimSpace(htmlquery.InnerText(title))
	log.Infof("Found title: %s", deckName)

	// Find the download URL
	a := htmlquery.FindOne(doc, fileXPath)
	if a == nil {
		return nil, fmt.Errorf("no download link found in %s (XPath: %s)", url, fileXPath)
	}
	fileURL := baseURL + htmlquery.InnerText(a)
	log.Infof("Found file URL: %s", fileURL)

	return queryDeckFile(fileURL, deckName, options)
}

type manaStackDeckOwner struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type manaStackSet struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func handleMoxfieldLink(baseURL string, options map[string]string) (decks []*plugins.Deck, err error) {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	deckID := path.Base(parsedURL.Path)
	titleXPath := `//title`
	fileURL := "https://api.moxfield.com/v1/decks/all/" + deckID + "/download"

	log.Infof("Checking %s", baseURL)
	doc, err := htmlquery.LoadURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("couldn't query %s: %w", baseURL, err)
	}

	// Find the title
	title := htmlquery.FindOne(doc, titleXPath)
	if title == nil {
		return nil, fmt.Errorf("no title found in %s (XPath: %s)", baseURL, titleXPath)
	}
	titleText := htmlquery.InnerText(title)
	deckName := strings.TrimSpace(strings.Split(titleText, "—")[0])

	log.Infof("Found title: %s", deckName)

	return queryDeckFile(fileURL, deckName, options)
}

type manaStackCardInfo struct {
	Name string       `json:"name"`
	Set  manaStackSet `json:"set"`
}

type manaStackCard struct {
	Card       manaStackCardInfo `json:"card"`
	Commander  bool              `json:"commander"`
	Sideboard  bool              `json:"sideboard"`
	Maybeboard bool              `json:"maybeboard"`
}

type manaStackDeck struct {
	Cards []manaStackCard    `json:"cards"`
	Name  string             `json:"name"`
	Owner manaStackDeckOwner `json:"owner"`
}

func handleManaStackLink(baseURL string, options map[string]string) (decks []*plugins.Deck, err error) {
	log.Infof("Checking %s", baseURL)

	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	slug := path.Base(parsedURL.Path)
	deckInfoURL := "https://manastack.com/api/deck?slug=" + slug

	// Build the request
	req, err := http.NewRequest("GET", deckInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("couldn't create request for %s: %w", deckInfoURL, err)
	}

	client := &http.Client{}

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't query %s: %w", deckInfoURL, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("couldn't close the response body: %w", cerr)
		}
	}()

	data := manaStackDeck{}

	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse response from %s: %w", deckInfoURL, err)
	}
	deckName := data.Name

	commanders := make([]string, 0, 2)
	main := make([]string, 0, len(data.Cards))
	sideboard := make([]string, 0, len(data.Cards))
	maybeboard := make([]string, 0, len(data.Cards))

	for _, card := range data.Cards {
		if card.Commander {
			commanders = append(commanders, card.Card.Name)
		} else if card.Sideboard {
			sideboard = append(sideboard, card.Card.Name)
		} else if card.Maybeboard {
			maybeboard = append(maybeboard, card.Card.Name)
		} else {
			main = append(main, card.Card.Name)
		}
	}

	var sb strings.Builder

	printCards := func(sb *strings.Builder, cards []string) {
		for _, card := range cards {
			sb.WriteString("1 ")
			sb.WriteString(card)
			sb.WriteString("\n")
		}
	}
	printCards(&sb, commanders)
	printCards(&sb, main)
	if len(sideboard) > 0 {
		sb.WriteString("Sideboard\n")
	}
	printCards(&sb, sideboard)
	if len(maybeboard) > 0 {
		sb.WriteString("Maybeboard\n")
	}
	printCards(&sb, maybeboard)

	return fromDeckFile(strings.NewReader(sb.String()), deckName, options)
}

type archidektOwner struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Avatar   string `json:"avatar"`
}

type archidektOracleCard struct {
	Name string `json:"name"`
}

type archidektEdition struct {
	Code     string `json:"editioncode"`
	Name     string `json:"editionname"`
	MTGOCode string `json:"mtgoCode"`
}

type archidektCardInfo struct {
	SkryfallID string              `json:"uid"`
	OracleCard archidektOracleCard `json:"oracleCard"`
	Edition    archidektEdition    `json:"edition"`
}

type archidektCard struct {
	Card     archidektCardInfo `json:"card"`
	Quantity int               `json:"quantity"`
	Modifier string            `json:"modifier"`
	Category string            `json:"category"`
	Label    string            `json:"label"`
}

type archidektDeck struct {
	ID          int             `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Owner       archidektOwner  `json:"owner"`
	Cards       []archidektCard `json:"cards"`
}

func handleArchidektLink(baseURL string, options map[string]string) (decks []*plugins.Deck, err error) {
	log.Infof("Checking %s", baseURL)

	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	id := path.Base(parsedURL.Path)
	deckInfoURL := "https://archidekt.com/api/decks/" + id + "/small/"

	// Build the request
	req, err := http.NewRequest("GET", deckInfoURL, nil)
	if err != nil {
		return nil, fmt.Errorf("couldn't create request for %s: %w", deckInfoURL, err)
	}

	client := &http.Client{}

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't query %s: %w", deckInfoURL, err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("couldn't close the response body: %w", cerr)
		}
	}()

	data := archidektDeck{}

	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse response from %s: %w", deckInfoURL, err)
	}
	deckName := data.Name

	commanders := make([]archidektCard, 0, 2)
	main := make([]archidektCard, 0, len(data.Cards))
	sideboard := make([]archidektCard, 0, len(data.Cards))
	maybeboard := make([]archidektCard, 0, len(data.Cards))

	for _, card := range data.Cards {
		switch card.Category {
		case "Commander":
			commanders = append(commanders, card)
		case "Sideboard":
			sideboard = append(sideboard, card)
		case "Maybeboard":
			maybeboard = append(maybeboard, card)
		default:
			main = append(main, card)
		}
	}

	var sb strings.Builder

	printCards := func(sb *strings.Builder, cards []archidektCard) {
		for _, card := range cards {
			sb.WriteString(strconv.Itoa(card.Quantity))
			sb.WriteString(" ")
			sb.WriteString(card.Card.OracleCard.Name)
			sb.WriteString(" (")
			sb.WriteString(strings.ToUpper(card.Card.Edition.Code))
			sb.WriteString(")")
			sb.WriteString("\n")
		}
	}
	printCards(&sb, commanders)
	printCards(&sb, main)
	if len(sideboard) > 0 {
		sb.WriteString("Sideboard\n")
	}
	printCards(&sb, sideboard)
	if len(maybeboard) > 0 {
		sb.WriteString("Maybeboard\n")
	}
	printCards(&sb, maybeboard)

	return fromDeckFile(strings.NewReader(sb.String()), deckName, options)
}

type frogtownDeckDetails struct {
	ID             string            `json:"_id"`
	Name           string            `json:"name"`
	OwnerID        string            `json:"ownerID"`
	Mainboard      []string          `json:"mainboard"`
	Sideboard      []string          `json:"sideboard"`
	IDToNameSubset map[string]string `json:"IDToNameSubset"`
}

type frogtownData struct {
	DeckDetails frogtownDeckDetails `json:"deckDetails"`
}

func handleFrogtownLink(baseURL string, options map[string]string) (decks []*plugins.Deck, err error) {
	scriptXPath := `//body/script[not(@src)]`

	log.Infof("Checking %s", baseURL)
	doc, err := htmlquery.LoadURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("couldn't query %s: %w", baseURL, err)
	}

	// Find the script tag
	scriptTags := htmlquery.Find(doc, scriptXPath)
	if scriptTags == nil {
		return nil, fmt.Errorf("no script tag found in %s (XPath: %s)", baseURL, scriptXPath)
	}

	const (
		scriptPrefix = "var includedData = "
		scriptSuffix = ";"
	)
	var jsonData string

	for _, scriptTag := range scriptTags {
		scriptContents := strings.TrimSpace(htmlquery.InnerText(scriptTag))
		if strings.HasPrefix(scriptContents, scriptPrefix) {
			jsonData = strings.TrimSuffix(
				strings.TrimPrefix(
					scriptContents,
					scriptPrefix,
				),
				scriptSuffix,
			)
			break
		}
	}

	if len(jsonData) == 0 {
		return nil, fmt.Errorf("no includedData found in %s", baseURL)
	}

	var data frogtownData

	err = json.Unmarshal([]byte(jsonData), &data)
	if err != nil {
		return nil, fmt.Errorf("couldn't parse includedData from %s: %w", baseURL, err)
	}

	deckName := data.DeckDetails.Name

	var sb strings.Builder

	printCards := func(sb *strings.Builder, cards []string) {
		for _, card := range cards {
			name, ok := data.DeckDetails.IDToNameSubset[card]
			if !ok {
				log.Warnf("card ID %s not found in IDToNameSubset: %v", card, data.DeckDetails.IDToNameSubset)
				continue
			}
			sb.WriteString("1 ")
			sb.WriteString(name)
			sb.WriteString("\n")
		}
	}
	printCards(&sb, data.DeckDetails.Mainboard)
	if len(data.DeckDetails.Sideboard) > 0 {
		sb.WriteString("Sideboard\n")
	}
	printCards(&sb, data.DeckDetails.Sideboard)

	return fromDeckFile(strings.NewReader(sb.String()), deckName, options)
}

func handleCubeTutorLink(doc *html.Node, baseURL string, deckName string, cardSetXPath string, cardsXPath string, options map[string]string) (decks []*plugins.Deck, err error) {
	cardSets := htmlquery.Find(doc, cardSetXPath)
	main := make([]string, 0, 560)
	sideboard := make([]string, 0, 30)
	maybeboard := make([]string, 0, 30)

	for i, cardSet := range cardSets {
		cards := htmlquery.Find(cardSet, cardsXPath)

		for _, card := range cards {
			contents := htmlquery.InnerText(card)
			filename := path.Base(contents)
			cardSlug := strings.TrimSuffix(filename, filepath.Ext(filename))
			cardName, err := url.PathUnescape(cardSlug)

			// Fix for land names
			if strings.HasSuffix(cardName, "1") {
				cardName = cardName[:len(cardName)-1]
			}
			if err != nil {
				log.Warnf("Invalid card slug %s extracted from element \"%s\"", cardSlug, contents)
				continue
			}

			switch i {
			case 0:
				main = append(main, cardName)
			case 1:
				sideboard = append(sideboard, cardName)
			default:
				maybeboard = append(maybeboard, cardName)
			}
		}
	}

	var sb strings.Builder

	printCards := func(sb *strings.Builder, cards []string) {
		for _, card := range cards {
			sb.WriteString("1 ")
			sb.WriteString(card)
			sb.WriteString("\n")
		}
	}
	printCards(&sb, main)
	if len(sideboard) > 0 {
		sb.WriteString("Sideboard\n")
	}
	printCards(&sb, sideboard)
	if len(maybeboard) > 0 {
		sb.WriteString("Maybeboard\n")
	}
	printCards(&sb, maybeboard)

	return fromDeckFile(strings.NewReader(sb.String()), deckName, options)
}

func handleCubeCobraLink(baseURL string, options map[string]string) (decks []*plugins.Deck, err error) {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}

	slug := path.Base(parsedURL.Path)
	titleXPath := `//title`
	fileURL := "https://cubecobra.com/cube/download/mtgo/" + slug

	log.Infof("Checking %s", baseURL)
	doc, err := htmlquery.LoadURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("couldn't query %s: %w", baseURL, err)
	}

	// Find the title
	title := htmlquery.FindOne(doc, titleXPath)
	if title == nil {
		return nil, fmt.Errorf("no title found in %s (XPath: %s)", baseURL, titleXPath)
	}
	titleText := htmlquery.InnerText(title)
	deckName := strings.TrimSpace(strings.Split(titleText, "-")[0])

	log.Infof("Found title: %s", deckName)

	return queryDeckFile(fileURL, deckName, options)
}
