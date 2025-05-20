// File: sitemap.go
// Description: Fournit les structures et fonctions nécessaires pour générer
//
//	un fichier sitemap.xml conforme au protocole sitemap standard.
package blitzkit

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"time"
)

// SitemapEntry représente une entrée URL unique dans un sitemap XML.
// Contient l'URL (loc), la date de dernière modification (lastmod),
// la fréquence de changement (changefreq), et la priorité.
type SitemapEntry struct {
	XMLName    xml.Name   `xml:"url"`
	URL        string     `xml:"loc"`
	LastMod    *time.Time `xml:"lastmod,omitempty"`
	ChangeFreq string     `xml:"changefreq,omitempty"`
	Priority   float32    `xml:"priority,omitempty"`
}

// SitemapChangeFreqAlways indique que le contenu change à chaque accès.
const SitemapChangeFreqAlways = "always"

// SitemapChangeFreqHourly indique que le contenu change environ toutes les heures.
const SitemapChangeFreqHourly = "hourly"

// SitemapChangeFreqDaily indique que le contenu change environ tous les jours.
const SitemapChangeFreqDaily = "daily"

// SitemapChangeFreqWeekly indique que le contenu change environ toutes les semaines.
const SitemapChangeFreqWeekly = "weekly"

// SitemapChangeFreqMonthly indique que le contenu change environ tous les mois.
const SitemapChangeFreqMonthly = "monthly"

// SitemapChangeFreqYearly indique que le contenu change environ tous les ans.
const SitemapChangeFreqYearly = "yearly"

// SitemapChangeFreqNever indique que le contenu est archivé et ne change jamais.
const SitemapChangeFreqNever = "never"

// urlset est l'élément racine du document sitemap XML.
// Il contient l'attribut de namespace requis et une liste d'éléments <url>.
type urlset struct {
	XMLName xml.Name       `xml:"urlset"`
	Xmlns   string         `xml:"xmlns,attr"`
	URLs    []SitemapEntry `xml:"url"`
}

// GenerateSitemapXMLBytes génère le contenu complet d'un fichier sitemap.xml
// sous forme de slice de bytes, à partir d'une liste d'entrées SitemapEntry.
// Inclut l'en-tête XML et l'élément racine <urlset> avec le namespace correct.
//
// Args:
//
//	entries ([]SitemapEntry): La liste des entrées URL à inclure dans le sitemap.
//
// Returns:
//
//	([]byte, error): La slice de bytes contenant le XML généré, ou nil et une erreur si l'encodage échoue.
func GenerateSitemapXMLBytes(entries []SitemapEntry) ([]byte, error) {
	if entries == nil {
		entries = []SitemapEntry{}
	}

	sitemap := urlset{
		Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs:  entries,
	}

	var buf bytes.Buffer
	buf.WriteString(xml.Header)

	encoder := xml.NewEncoder(&buf)
	encoder.Indent("", "  ")

	if err := encoder.Encode(sitemap); err != nil {
		return nil, fmt.Errorf("failed to encode sitemap XML: %w", err)
	}

	return buf.Bytes(), nil
}
