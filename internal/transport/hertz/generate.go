package hertz

// Regenerate the HTTP DTOs from the product-owned Thrift contract. The slim
// template intentionally omits Apache Thrift RPC serializers and processors;
// this service currently uses Thrift as its cross-language HTTP schema.
//
//go:generate hz model --module github.com/qiuyier/omni-agent-customer-service --idl ../../../idl/customer_service.thrift --out_dir ../../.. --model_dir internal/transport/hertz --thriftgo template=slim
