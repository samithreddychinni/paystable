package adapters

import (
	"github.com/IDEA-Amrita/paystable/internal/config"
	"github.com/IDEA-Amrita/paystable/internal/gateway"
	"github.com/IDEA-Amrita/paystable/internal/gateway/payu"
	"github.com/IDEA-Amrita/paystable/internal/gateway/razorpay"
)

func New(cfg *config.Config) map[string]gateway.Adapter {
	return map[string]gateway.Adapter{
		"payu":     payu.NewClient(cfg.PayuStatusURL, cfg.GatewayAPIKey, cfg.WebhookSecret),
		"razorpay": razorpay.NewClient(cfg.RazorpayAPIURL, cfg.RazorpayKeyID, cfg.RazorpayKeySecret),
	}
}
