package proto

import (
	"testing"

	_ "github.com/asao-platform/atoc/packages/proto/booking/v1"
	_ "github.com/asao-platform/atoc/packages/proto/common/v1"
	_ "github.com/asao-platform/atoc/packages/proto/employee/v1"
	_ "github.com/asao-platform/atoc/packages/proto/identity/v1"
	_ "github.com/asao-platform/atoc/packages/proto/media/v1"
	_ "github.com/asao-platform/atoc/packages/proto/notification/v1"
	_ "github.com/asao-platform/atoc/packages/proto/organization/v1"
	_ "github.com/asao-platform/atoc/packages/proto/website/v1"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func TestProtoSpecifications(t *testing.T) {
	// Verify approved services exist
	verifyServiceExists(t, "identity.v1.IdentityService")
	verifyServiceExists(t, "booking.v1.BookingService")
	verifyServiceExists(t, "organization.v1.OrganizationService")

	// Verify unapproved domains do not define services
	verifyServiceDoesNotExist(t, "employee.v1.EmployeeService")
	verifyServiceDoesNotExist(t, "media.v1.MediaService")
	verifyServiceDoesNotExist(t, "notification.v1.NotificationService")
	verifyServiceDoesNotExist(t, "website.v1.WebsiteService")

	// Verify common RequestContext uses organization_id, not tenant ID
	verifyMessageFieldExists(t, "common.v1.RequestContext", "organization_id")
	verifyMessageFieldDoesNotExist(t, "common.v1.RequestContext", "tenant"+"_id")

	// Verify AuthenticateStaffWithGoogleRequest fields
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithGoogleRequest", "organization_id")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithGoogleRequest", "google_id_token")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithGoogleRequest", "correlation_id")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithGoogleRequest", "causation_id")
	verifyMessageFieldDoesNotExist(t, "identity.v1.AuthenticateStaffWithGoogleRequest", "tenant"+"_id")
	verifyMessageFieldDoesNotExist(t, "identity.v1.AuthenticateStaffWithGoogleRequest", "email")
	verifyMessageFieldDoesNotExist(t, "identity.v1.AuthenticateStaffWithGoogleRequest", "google_sub")

	// Verify AuthenticateStaffWithGoogleResponse fields
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithGoogleResponse", "user_id")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithGoogleResponse", "user_type")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithGoogleResponse", "organization_id")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithGoogleResponse", "access_token")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithGoogleResponse", "created")
	verifyMessageFieldDoesNotExist(t, "identity.v1.AuthenticateStaffWithGoogleResponse", "tenant"+"_id")
	verifyMessageFieldDoesNotExist(t, "identity.v1.AuthenticateStaffWithGoogleResponse", "email")
	verifyMessageFieldDoesNotExist(t, "identity.v1.AuthenticateStaffWithGoogleResponse", "google_sub")

	// Verify the Keycloak staff front door keeps the existing staff auth response shape.
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithKeycloakRequest", "organization_id")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithKeycloakRequest", "asao_user_id")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithKeycloakRequest", "correlation_id")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithKeycloakRequest", "causation_id")
	verifyMessageFieldDoesNotExist(t, "identity.v1.AuthenticateStaffWithKeycloakRequest", "google_id_token")
	verifyMessageFieldDoesNotExist(t, "identity.v1.AuthenticateStaffWithKeycloakRequest", "tenant"+"_id")

	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithKeycloakResponse", "user_id")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithKeycloakResponse", "user_type")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithKeycloakResponse", "organization_id")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithKeycloakResponse", "access_token")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithKeycloakResponse", "created")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithKeycloakResponse", "refresh_token")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithKeycloakResponse", "access_token_expires_at")
	verifyMessageFieldExists(t, "identity.v1.AuthenticateStaffWithKeycloakResponse", "refresh_token_expires_at")

	verifyMessageFieldExists(t, "identity.v1.ValidateTokenResponse", "session_class")
	verifyMessageFieldExists(t, "identity.v1.ProvisionOwnerStaffRequest", "organization_id")
	verifyMessageFieldExists(t, "identity.v1.ProvisionOwnerStaffRequest", "asao_user_id")
	verifyMessageFieldExists(t, "identity.v1.ProvisionOwnerStaffResponse", "staff_id")
	verifyMessageFieldExists(t, "identity.v1.ProvisionOwnerStaffResponse", "created")

	verifyMessageFieldExists(t, "organization.v1.Organization", "id")
	verifyMessageFieldExists(t, "organization.v1.Organization", "slug")
	verifyMessageFieldExists(t, "organization.v1.Organization", "created_by_asao_user_id")
	verifyMessageFieldExists(t, "organization.v1.CreateOrganizationRequest", "correlation_id")
	verifyMessageFieldExists(t, "organization.v1.CreateOrganizationRequest", "causation_id")
}

func verifyServiceExists(t *testing.T, fullName string) {
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(fullName))
	if err != nil {
		t.Fatalf("expected service %s to exist, but got error: %v", fullName, err)
	}
	if _, ok := desc.(protoreflect.ServiceDescriptor); !ok {
		t.Fatalf("expected %s to be a ServiceDescriptor", fullName)
	}
}

func verifyServiceDoesNotExist(t *testing.T, fullName string) {
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(fullName))
	if err == nil {
		if _, ok := desc.(protoreflect.ServiceDescriptor); ok {
			t.Fatalf("unapproved service %s must not be defined", fullName)
		}
	}
}

func verifyMessageFieldExists(t *testing.T, messageFullName, fieldName string) {
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(messageFullName))
	if err != nil {
		t.Fatalf("expected message %s to exist: %v", messageFullName, err)
	}
	msgDesc, ok := desc.(protoreflect.MessageDescriptor)
	if !ok {
		t.Fatalf("%s is not a MessageDescriptor", messageFullName)
	}
	field := msgDesc.Fields().ByName(protoreflect.Name(fieldName))
	if field == nil {
		t.Fatalf("expected field %s in message %s to exist", fieldName, messageFullName)
	}
}

func verifyMessageFieldDoesNotExist(t *testing.T, messageFullName, fieldName string) {
	desc, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(messageFullName))
	if err != nil {
		return // Message doesn't exist, which is fine
	}
	msgDesc, ok := desc.(protoreflect.MessageDescriptor)
	if !ok {
		return
	}
	field := msgDesc.Fields().ByName(protoreflect.Name(fieldName))
	if field != nil {
		t.Fatalf("field %s in message %s must not exist", fieldName, messageFullName)
	}
}
