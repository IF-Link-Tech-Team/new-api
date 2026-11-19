package authz

const ResourceAdministration = "administration"

var GlobalAdminAccess = Permission{Resource: ResourceAdministration, Action: "access"}

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceAdministration,
		LabelKey: "Global administration",
		Actions: []ActionDefinition{{
			Action:         "access",
			LabelKey:       "Access global administration",
			DescriptionKey: "Turn off to limit management to explicitly granted groups. Internal model access is preserved.",
			DefaultRoles:   []string{BuiltInRoleAdmin},
		}},
	})
}
