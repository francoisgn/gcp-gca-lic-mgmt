package gcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	procurement "cloud.google.com/go/commerce/consumer/procurement/apiv1"
	"cloud.google.com/go/commerce/consumer/procurement/apiv1/procurementpb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

// Procurement is the real Licensing implementation, based on the official Go
// client of the Cloud Commerce Consumer Procurement API (REST transport).
//
// Required permissions on the billing account (custom role, see Terraform):
//
//	consumerprocurement.orders.list, consumerprocurement.orders.get,
//	consumerprocurement.licensePools.get, consumerprocurement.licensePools.assign,
//	consumerprocurement.licensePools.unassign,
//	consumerprocurement.licensePools.enumerateLicensedUsers, billing.accounts.get
//
// Individual assignment only works when the pool uses the MANUAL assignment
// protocol: the portal is the only assignment authority, do not switch the
// pool to automatic assignment in the console.
type Procurement struct {
	billingAccount string
	orderIDs       []string // optional explicit list of orders (short IDs)
	orders         *procurement.ConsumerProcurementClient
	licenses       *procurement.LicenseManagementClient
}

// NewProcurement creates the API clients with Application Default Credentials
// (the Cloud Run service account in production).
func NewProcurement(ctx context.Context, billingAccount, quotaProject string, orderIDs []string) (*Procurement, error) {
	var opts []option.ClientOption
	if quotaProject != "" {
		opts = append(opts, option.WithQuotaProject(quotaProject))
	}
	orders, err := procurement.NewConsumerProcurementRESTClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("procurement client: %w", err)
	}
	licenses, err := procurement.NewLicenseManagementRESTClient(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("license management client: %w", err)
	}
	return &Procurement{
		billingAccount: strings.TrimPrefix(billingAccount, "billingAccounts/"),
		orderIDs:       orderIDs,
		orders:         orders,
		licenses:       licenses,
	}, nil
}

// Close releases the clients.
func (p *Procurement) Close() error {
	_ = p.orders.Close()
	return p.licenses.Close()
}

// ListOrders implements Licensing.
func (p *Procurement) ListOrders(ctx context.Context) ([]Order, error) {
	parent := "billingAccounts/" + p.billingAccount
	var out []Order
	if len(p.orderIDs) > 0 {
		// Explicit list: fetch each order directly.
		for _, id := range p.orderIDs {
			o, err := p.orders.GetOrder(ctx, &procurementpb.GetOrderRequest{Name: parent + "/orders/" + id})
			if err != nil {
				return nil, err
			}
			out = append(out, toOrder(o))
		}
		return out, nil
	}
	it := p.orders.ListOrders(ctx, &procurementpb.ListOrdersRequest{Parent: parent})
	for {
		o, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		out = append(out, toOrder(o))
	}
	return out, nil
}

// toOrder flattens an order: the subscription window is taken from the
// first non-cancelled line item that carries one.
func toOrder(o *procurementpb.Order) Order {
	out := Order{Name: o.GetName(), DisplayName: o.GetDisplayName(), Cancelled: len(o.GetLineItems()) == 0}
	if o.GetCreateTime() != nil {
		out.CreateTime = o.GetCreateTime().AsTime()
	}
	for _, li := range o.GetLineItems() {
		info := li.GetLineItemInfo()
		if info == nil {
			continue
		}
		if out.Offer == "" {
			out.Offer = info.GetOffer()
		}
		if sub := info.GetSubscription(); sub != nil {
			if sub.GetStartTime() != nil {
				t := sub.GetStartTime().AsTime()
				out.Start = &t
			}
			if sub.GetEndTime() != nil {
				t := sub.GetEndTime().AsTime()
				out.End = &t
			}
			out.AutoRenew = sub.GetAutoRenewalEnabled()
			break
		}
	}
	return out
}

// GetPool implements Licensing.
func (p *Procurement) GetPool(ctx context.Context, order string) (Pool, error) {
	lp, err := p.licenses.GetLicensePool(ctx, &procurementpb.GetLicensePoolRequest{Name: PoolName(order)})
	if err != nil {
		return Pool{}, err
	}
	return Pool{
		Name:      lp.GetName(),
		Total:     int(lp.GetTotalLicenseCount()),
		Available: int(lp.GetAvailableLicenseCount()),
		Manual:    lp.GetLicenseAssignmentProtocol().GetManualAssignmentType() != nil,
	}, nil
}

// Enumerate implements Licensing.
func (p *Procurement) Enumerate(ctx context.Context, order string) ([]LicensedUser, error) {
	it := p.licenses.EnumerateLicensedUsers(ctx, &procurementpb.EnumerateLicensedUsersRequest{
		Parent:   PoolName(order),
		PageSize: 500,
	})
	var out []LicensedUser
	for {
		u, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		lu := LicensedUser{Email: strings.ToLower(u.GetUsername())}
		if u.GetAssignTime() != nil {
			lu.AssignTime = u.GetAssignTime().AsTime()
		}
		if u.GetRecentUsageTime() != nil {
			t := u.GetRecentUsageTime().AsTime()
			if t.After(time.Unix(0, 0)) {
				lu.RecentUsage = &t
			}
		}
		out = append(out, lu)
	}
	return out, nil
}

// Assign implements Licensing.
func (p *Procurement) Assign(ctx context.Context, order string, emails []string) error {
	_, err := p.licenses.Assign(ctx, &procurementpb.AssignRequest{Parent: PoolName(order), Usernames: emails})
	return err
}

// Unassign implements Licensing.
func (p *Procurement) Unassign(ctx context.Context, order string, emails []string) error {
	_, err := p.licenses.Unassign(ctx, &procurementpb.UnassignRequest{Parent: PoolName(order), Usernames: emails})
	return err
}
