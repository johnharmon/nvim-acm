package catalog

type TemplateParam struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Optional    bool   `json:"optional,omitempty"`
	Variadic    bool   `json:"variadic,omitempty"`
}

type TemplateReturn struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type TemplateFunction struct {
	Name        string          `json:"name"`
	Signature   string          `json:"signature"`
	Params      []TemplateParam `json:"params"`
	Returns     TemplateReturn  `json:"returns"`
	Description string          `json:"description"`
	Since       string          `json:"since,omitempty"`
	Deprecated  string          `json:"deprecated,omitempty"`
	Examples    []string        `json:"examples,omitempty"`
	DocURL      string          `json:"docUrl,omitempty"`
	Source      string          `json:"source,omitempty"`
}

type ExportedValue struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Since       string `json:"since,omitempty"`
	DocURL      string `json:"docUrl,omitempty"`
	Source      string `json:"source,omitempty"`
}

type AcmCatalog struct {
	AcmVersion            string             `json:"acmVersion"`
	HubFunctions          []TemplateFunction `json:"hubFunctions"`
	ManagedFunctions      []TemplateFunction `json:"managedFunctions"`
	SprigFunctions        []TemplateFunction `json:"sprigFunctions"`
	HubExportedValues     []ExportedValue    `json:"hubExportedValues"`
	ManagedExportedValues []ExportedValue    `json:"managedExportedValues"`
}

type HelmCatalog struct {
	Source    string             `json:"source"`
	Functions []TemplateFunction `json:"functions"`
}

type Resolved struct {
	AcmVersion            string
	HelmFunctions         []TemplateFunction
	HubFunctions          []TemplateFunction
	ManagedFunctions      []TemplateFunction
	SprigFunctions        []TemplateFunction
	GoBuiltins            []TemplateFunction
	HubExportedValues     []ExportedValue
	ManagedExportedValues []ExportedValue
}

type UserExtras struct {
	HelmFunctions         []TemplateFunction
	HubFunctions          []TemplateFunction
	ManagedFunctions      []TemplateFunction
	SprigFunctions        []TemplateFunction
	HubExportedValues     []ExportedValue
	ManagedExportedValues []ExportedValue
}
