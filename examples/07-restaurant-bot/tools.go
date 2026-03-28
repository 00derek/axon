// examples/07-restaurant-bot/tools.go
//
// Restaurant domain tool definitions for the restaurant bot example.
// Each tool is built with kernel.NewTool and uses typed parameter structs
// with json and description struct tags for schema generation.
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/axonframework/axon/kernel"
)

// ---------------------------------------------------------------------------
// Parameter structs
// ---------------------------------------------------------------------------

// SearchRestaurantsParams holds parameters for the search_restaurants tool.
type SearchRestaurantsParams struct {
	Query    string `json:"query"    description:"Cuisine type or restaurant name to search for"`
	Location string `json:"location" description:"Neighborhood or area to search within"`
}

// GetWeatherParams holds parameters for the get_weather tool.
type GetWeatherParams struct {
	Location string `json:"location" description:"City or neighborhood to get weather for"`
}

// GetMenuParams holds parameters for the get_menu tool.
type GetMenuParams struct {
	Restaurant string `json:"restaurant" description:"Name of the restaurant whose menu to retrieve"`
}

// MakeReservationParams holds parameters for the make_reservation tool.
type MakeReservationParams struct {
	Restaurant string `json:"restaurant"  description:"Name of the restaurant to reserve at"`
	PartySize  int    `json:"party_size"  description:"Number of guests in the party" minimum:"1" maximum:"20"`
	Time       string `json:"time"        description:"Requested reservation time, e.g. '7:00 PM'"`
}

// ---------------------------------------------------------------------------
// Result types
// ---------------------------------------------------------------------------

// Restaurant is a single search result entry.
type Restaurant struct {
	Name    string  `json:"name"`
	Cuisine string  `json:"cuisine"`
	Rating  float64 `json:"rating"`
	Price   string  `json:"price"`
}

// WeatherInfo describes current outdoor conditions.
type WeatherInfo struct {
	TemperatureF int    `json:"temperature_f"`
	Condition    string `json:"condition"`
	OutdoorOK    bool   `json:"outdoor_ok"`
}

// MenuItem is a single dish on a restaurant menu.
type MenuItem struct {
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

// Reservation is the result of a successful (or pending) booking.
type Reservation struct {
	Confirmed    bool   `json:"confirmed"`
	Restaurant   string `json:"restaurant"`
	PartySize    int    `json:"party_size"`
	Time         string `json:"time"`
	Confirmation string `json:"confirmation"`
}

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

var mockRestaurants = []Restaurant{
	{Name: "Bella Trattoria", Cuisine: "Italian", Rating: 4.7, Price: "$$"},
	{Name: "Sakura Garden", Cuisine: "Japanese", Rating: 4.5, Price: "$$$"},
	{Name: "The Taco Stand", Cuisine: "Mexican", Rating: 4.3, Price: "$"},
	{Name: "Le Petit Bistro", Cuisine: "French", Rating: 4.8, Price: "$$$$"},
	{Name: "Spice Route", Cuisine: "Indian", Rating: 4.6, Price: "$$"},
	{Name: "Harbor Grill", Cuisine: "Seafood", Rating: 4.4, Price: "$$$"},
	{Name: "Dragon Palace", Cuisine: "Chinese", Rating: 4.2, Price: "$$"},
}

var mockMenus = map[string][]MenuItem{
	"bella trattoria": {
		{Name: "Margherita Pizza", Price: 14.00},
		{Name: "Fettuccine Alfredo", Price: 18.00},
		{Name: "Tiramisu", Price: 8.00},
		{Name: "Bruschetta", Price: 9.00},
	},
	"sakura garden": {
		{Name: "Dragon Roll", Price: 16.00},
		{Name: "Miso Soup", Price: 4.00},
		{Name: "Salmon Sashimi", Price: 22.00},
		{Name: "Edamame", Price: 5.00},
	},
	"the taco stand": {
		{Name: "Street Tacos (3)", Price: 10.00},
		{Name: "Chips & Guacamole", Price: 6.00},
		{Name: "Burrito Bowl", Price: 12.00},
	},
}

// ---------------------------------------------------------------------------
// Tool constructors
// ---------------------------------------------------------------------------

// NewSearchRestaurantsTool returns a tool that searches mock restaurant data
// by cuisine type or name and returns a filtered list with guidance on
// how to present the results.
func NewSearchRestaurantsTool() kernel.Tool {
	return kernel.NewTool[SearchRestaurantsParams, kernel.Guided[[]Restaurant]](
		"search_restaurants",
		"Search for restaurants by cuisine type or name. Returns a list of matching restaurants with ratings and price ranges.",
		func(_ context.Context, p SearchRestaurantsParams) (kernel.Guided[[]Restaurant], error) {
			query := strings.ToLower(p.Query)
			var matches []Restaurant
			for _, r := range mockRestaurants {
				if strings.Contains(strings.ToLower(r.Name), query) ||
					strings.Contains(strings.ToLower(r.Cuisine), query) {
					matches = append(matches, r)
				}
			}
			if len(matches) == 0 {
				// Return all restaurants when nothing specific matches
				matches = mockRestaurants
			}
			return kernel.Guide(
				matches,
				"Present these restaurants in a friendly, readable list. "+
					"Highlight ratings and price range ($ = budget, $$$$ = fine dining). "+
					"Offer to get the menu or make a reservation for any of them.",
			), nil
		},
	)
}

// NewGetWeatherTool returns a tool that provides mock weather information
// for a given location to help the user decide on indoor vs outdoor dining.
func NewGetWeatherTool() kernel.Tool {
	return kernel.NewTool[GetWeatherParams, WeatherInfo](
		"get_weather",
		"Get current weather conditions for a location to help decide between indoor and outdoor dining.",
		func(_ context.Context, p GetWeatherParams) (WeatherInfo, error) {
			return WeatherInfo{
				TemperatureF: 72,
				Condition:    "Partly cloudy",
				OutdoorOK:    true,
			}, nil
		},
	)
}

// NewGetMenuTool returns a tool that retrieves a mock menu for a named restaurant.
func NewGetMenuTool() kernel.Tool {
	return kernel.NewTool[GetMenuParams, []MenuItem](
		"get_menu",
		"Retrieve the menu for a specific restaurant, including dish names and prices.",
		func(_ context.Context, p GetMenuParams) ([]MenuItem, error) {
			key := strings.ToLower(p.Restaurant)
			menu, ok := mockMenus[key]
			if !ok {
				return nil, fmt.Errorf("menu not found for restaurant %q", p.Restaurant)
			}
			return menu, nil
		},
	)
}

// NewMakeReservationTool returns a tool that books a mock reservation.
// Parties larger than 10 receive a Guided hint advising the user to call
// the restaurant directly to confirm large-group arrangements.
func NewMakeReservationTool() kernel.Tool {
	return kernel.NewTool[MakeReservationParams, kernel.Guided[Reservation]](
		"make_reservation",
		"Make a restaurant reservation for a specified party size and time. Returns a confirmation code.",
		func(_ context.Context, p MakeReservationParams) (kernel.Guided[Reservation], error) {
			code := fmt.Sprintf("RES-%s-%04d", strings.ToUpper(p.Restaurant[:3]), p.PartySize*100+len(p.Time))
			res := Reservation{
				Confirmed:    true,
				Restaurant:   p.Restaurant,
				PartySize:    p.PartySize,
				Time:         p.Time,
				Confirmation: code,
			}

			if p.PartySize > 10 {
				return kernel.Guide(
					res,
					"This is a large party reservation (more than 10 guests). "+
						"Inform the user their reservation is tentatively confirmed with code %s, "+
						"but strongly advise them to call the restaurant directly to confirm "+
						"special seating arrangements, deposit requirements, and menu options for large groups.",
					code,
				), nil
			}

			return kernel.Guide(
				res,
				"Reservation confirmed! Share the confirmation code %s with the user and remind them "+
					"to arrive a few minutes early. Offer to answer any other questions about the restaurant.",
				code,
			), nil
		},
	)
}

// ---------------------------------------------------------------------------
// AllTools
// ---------------------------------------------------------------------------

// AllTools returns all four restaurant bot tools ready for use with an agent.
func AllTools() []kernel.Tool {
	return []kernel.Tool{
		NewSearchRestaurantsTool(),
		NewGetWeatherTool(),
		NewGetMenuTool(),
		NewMakeReservationTool(),
	}
}
