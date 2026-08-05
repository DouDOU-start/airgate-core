package handler

import (
	appoauth "github.com/DouDOU-start/airgate-core/internal/app/oauth"
	"github.com/DouDOU-start/airgate-core/internal/server/dto"
)

func toOAuthClientResp(item appoauth.Client) dto.OAuthClientResp {
	resp := dto.OAuthClientResp{
		ID:            item.ID,
		ClientID:      item.ClientID,
		SecretHint:    item.SecretHint,
		Name:          item.Name,
		Description:   item.Description,
		RedirectURIs:  item.RedirectURIs,
		AllowedScopes: item.AllowedScopes,
		FirstParty:    item.FirstParty,
		Enabled:       item.Enabled,
		ShowInNav:     item.ShowInNav,
		LaunchURL:     item.LaunchURL,
		Icon:          item.Icon,
		SortOrder:     item.SortOrder,
	}
	resp.CreatedAt = item.CreatedAt
	resp.UpdatedAt = item.UpdatedAt
	return resp
}

func toOAuthClientSecretResp(item appoauth.Client, secret string) dto.OAuthClientSecretResp {
	return dto.OAuthClientSecretResp{
		OAuthClientResp: toOAuthClientResp(item),
		ClientSecret:    secret,
	}
}

func toAppEntryResp(item appoauth.Client) dto.AppEntryResp {
	return dto.AppEntryResp{
		Name:        item.Name,
		Description: item.Description,
		Icon:        item.Icon,
		LaunchURL:   item.LaunchURL,
	}
}

func toOAuthClientMutation(name, description string, redirectURIs, allowedScopes []string, firstParty, enabled, showInNav bool, launchURL, icon string, sortOrder int) appoauth.ClientMutation {
	return appoauth.ClientMutation{
		Name:          name,
		Description:   description,
		RedirectURIs:  redirectURIs,
		AllowedScopes: allowedScopes,
		FirstParty:    firstParty,
		Enabled:       enabled,
		ShowInNav:     showInNav,
		LaunchURL:     launchURL,
		Icon:          icon,
		SortOrder:     sortOrder,
	}
}

func toAuthorizeInput(req dto.AuthorizeReq) appoauth.AuthorizeInput {
	return appoauth.AuthorizeInput{
		ClientID:            req.ClientID,
		RedirectURI:         req.RedirectURI,
		Scope:               req.Scope,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: req.CodeChallengeMethod,
	}
}

func toTokenInput(req dto.OAuthTokenReq) appoauth.TokenInput {
	return appoauth.TokenInput{
		GrantType:    req.GrantType,
		Code:         req.Code,
		RedirectURI:  req.RedirectURI,
		ClientID:     req.ClientID,
		ClientSecret: req.ClientSecret,
		CodeVerifier: req.CodeVerifier,
	}
}
