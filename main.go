package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/oliamb/cutter"
	"github.com/otiai10/gosseract"
	"image"
	"image/jpeg"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
)

var (
	port    = "8080"
	etag    string
	tapJSON []byte
)

func init() {
	if "" != os.Getenv("PORT") {
		port = os.Getenv("PORT")
	}
	fmt.Println("Starting up, gathering Tap info for the first time")
	fmt.Println("This may take up to 60 seconds")
	getTapJSON()
	fmt.Println("Success. Tap info gathered")
}

// Cell location and size for a cell
type Cell struct {
	X      int
	Y      int
	Height int
	Width  int
}

// Tap defines a row/Tap for the image
type Tap struct {
	TapNumber    int
	Brewery      string
	Name         string
	Style        string
	Location     string
	ABV          string
	CrowlerPrice string
	GrowlerPrice string
	OnSale       bool
}

func getBeerListJPG() ([]byte, string) {
	// getBeerListJPG gets the AJ's Draft List JPG as []byte
	resp, err := http.Get("https://www.ajsbeerwarehouse.com/draft-list/")
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	bodyBytes, err := ioutil.ReadAll(resp.Body)
	re := regexp.MustCompile(`w,\s([^\s]+)\s`)
	listURL := re.FindAllSubmatch(bodyBytes, -1)
	jpeg := string(listURL[len(listURL)-1][1])
	fmt.Println("Found image URL: ", jpeg)
	resp, err = http.Get(jpeg)
	currentEtag := resp.Header.Get("etag")
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	bodyBytes, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Fatal(err)
	}
	return bodyBytes, currentEtag
}

func getImageGrid(image image.Image, xOffset int, yOffset int) ([]int, []int) {
	// getImageGrid looks for a black pixel at a specific offset
	// and returns a list of X coordinates and a list of Y coordinates
	if xOffset == 0 {
		xOffset = 1
	}
	if yOffset == 0 {
		yOffset = 1
	}
	var xArray []int
	xArray = append(xArray, 0)
	var yArray []int
	yArray = append(yArray, 0)
	bounds := image.Bounds()
	imageWidth, imageHeight := bounds.Max.X, bounds.Max.Y
	for x := xOffset; x < imageWidth; x++ {
		y := yOffset
		r, g, b, _ := image.At(x, y).RGBA()
		if r+g+b < 3000 {
			xArray = append(xArray, x)
		}
	}
	for y := yOffset; y < imageHeight; y++ {
		x := xOffset
		r, _, _, _ := image.At(x, y).RGBA()
		if r < 5000 {
			yArray = append(yArray, y)
		}
	}
	return xArray, yArray
}

func getImageCells(xArray []int, yArray []int) [][]Cell {
	// getImageCells returns a dict for each cell for an X list and Y list
	// of horizontal and vertical black lines
	var imageArray [][]Cell
	for i, row := range yArray[:len(yArray)-1] {
		// working on each row in the image
		// trim off the bottom row because it's random data in this case
		rowHeight := yArray[i+1] - yArray[i] - 1
		var rowArray []Cell
		for i, column := range xArray[:len(xArray)-1] {
			// working on each row in the column
			// ignoring the last line in the column
			// since the boxes are plotted based on their upper left point
			cellWidth := xArray[i+1] - xArray[i] - 1
			rowArray = append(rowArray, Cell{column + 1, row + 1, rowHeight, cellWidth})
		}
		imageArray = append(imageArray, rowArray)
	}
	return imageArray
}

func getTap(tapNumber int, tapArray []Cell, beerImage image.Image) Tap {
	// getTap converts a row into a Tap dict
	client := gosseract.NewClient()
	defer client.Close()
	var tap Tap
	tap.TapNumber = tapNumber
	for i, cell := range tapArray[1:] {
		croppedImg, err := cutter.Crop(beerImage, cutter.Config{
			Width:   cell.Width,
			Height:  cell.Height,
			Anchor:  image.Point{cell.X, cell.Y},
			Mode:    cutter.TopLeft, // optional, default value
			Options: cutter.Copy,
		})
		buff := new(bytes.Buffer)
		err = jpeg.Encode(buff, croppedImg, nil)
		if err != nil {
			fmt.Println("failed to create buffer", err)
		}
		client.SetImageFromBytes(buff.Bytes())
		text, err := client.Text()
		if err != nil {
			fmt.Println(err)
		}
		if text != "" {
			switch i {
			case 0:
				if strings.HasPrefix(text, "**") {
					tap.OnSale = true
					text = strings.ReplaceAll(text, "**", "")
				}
				tap.Brewery = strings.Replace(text, "\n", " ", -1)
			case 1:
				tap.Name = strings.Replace(text, "\n", " ", -1)
			case 2:
				tap.Style = strings.Replace(text, "\n", " ", -1)
			case 3:
				tap.Location = strings.Replace(text, "\n", " ", -1)
			case 4:
				// some ABVs get read wrong, like 75% instead of 7.5%
				// look to see if there's a decimal where we want it
				decimal := string(text[len(text)-3])
				if decimal != "." {
					text = string(text[0:len(text)-2]) + "." + string(text[len(text)-2:])
				}
				tap.ABV = text
			case 5:
				tap.CrowlerPrice = strings.Replace(text, " ", "", -1)
			case 6:
				tap.GrowlerPrice = strings.Replace(text, " ", "", -1)
			}
		}
	}
	return tap
}

func getTapJSON() []byte {
	beerListJPG, currentEtag := getBeerListJPG()
	if currentEtag != etag {
		etag = currentEtag
		beerListImage, _, err := image.Decode(bytes.NewReader(beerListJPG))
		if err != nil {
			log.Fatal(err)
		}
		xArray, yArray := getImageGrid(beerListImage, 3, 2)
		cells := getImageCells(xArray, yArray)
		if len(cells) > 0 {
			// drop the header and the footer
			cells = cells[1 : len(cells)-1]
		}
		var tapArray []Tap
		for i, row := range cells {
			tapNumber := i + 1
			tapArray = append(tapArray, getTap(tapNumber, row, beerListImage))
		}
		tapJSON, err = json.MarshalIndent(tapArray, "", "  ")
		if err != nil {
			fmt.Println("error:", err)
		}
		return tapJSON
	} else {
		return tapJSON
	}
}
func SendTapJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Content-Type", "application/json")
	tapJSON := getTapJSON()
	w.Write(tapJSON)
}

func main() {
	http.HandleFunc("/", SendTapJSON)
	http.ListenAndServe(":"+port, nil)
}
