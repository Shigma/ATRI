#include <node.h>
#include <uv.h>
#include <string.h>
#include <fstream>
#include <ios>
#include <functional>
#include <iostream>
#include "../src/main.h"

#define ENSURE_UV(x) assert(x == 0);

namespace ATRI {
	using v8::Exception;
	using v8::External;
	using v8::Context;
	using v8::Function;
	using v8::FunctionCallbackInfo;
	using v8::FunctionTemplate;
	using v8::Isolate;
	using v8::Local;
	using v8::MaybeLocal;
	using v8::Number;
	using v8::Integer;
	using v8::Object;
	using v8::ObjectTemplate;
	using v8::PropertyCallbackInfo;
	using v8::String;
	using v8::Value;

	/* Multi-instance test, not used yet */
	// class AddonContext
	// {
	// public:
	// 	Local<ObjectTemplate> tpl;
	// public:
	// 	AddonContext(Isolate* isolate): tpl(ObjectTemplate::New(isolate)) {
	// 		node::AddEnvironmentCleanupHook(isolate, Dispose, this);
	// 		tpl->SetAccessor(String::NewFromUtf8(isolate, "t1").ToLocalChecked(), GetPath);
	// 	}
	// 	static void GetPath(Local<String> name, const PropertyCallbackInfo<Value>& info) {
	// 		RequestData* request = UnwrapRequest(info.Holder());
	// 		const char* path = request->b;
	// 		info.GetReturnValue().Set(String::NewFromUtf8(info.GetIsolate(), path).ToLocalChecked());
	// 	}
	// 	static RequestData* UnwrapRequest(Local<Object> obj) {
	// 		Local<External> field = Local<External>::Cast(obj->GetInternalField(0));
	// 		void* ptr = field->Value();
	// 		return static_cast<RequestData*>(ptr);
	// 	}
	// 	~AddonContext() {
	// 	}
	// 	static void Dispose(void* arg) {
	// 		delete static_cast<AddonContext*>(arg);
	// 	}
	// private:
	// };

	std::string convert(Isolate* isolate, Local<String> str) {
		String::Utf8Value value(isolate, str);
		return *value;
	}

	template<typename T>
	T Convert(Isolate* isolate, Local<Value> value) {
		if constexpr (std::is_same_v<T, std::string>) {
			assert(value->IsString());
			String::Utf8Value value(isolate, value);
			return *value;
		}
		else if constexpr (std::is_same_v<T, bool>) {
			assert(value->IsBoolean());
			return value->BooleanValue(isolate);
		}
		else if constexpr (std::is_floating_point_v<T>) {
			assert(value->IsNumber());
			return value->NumberValue(isolate->GetCurrentContext()).FromJust();
		}
		else if constexpr (std::is_integral_v<T>) {
			assert(value->IsNumber());
			return value->IntegerValue(isolate->GetCurrentContext()).FromJust();
		}
		else if constexpr (std::is_same_v<T, Local<Function>>) {
			assert(value->IsFunction());
			return Local<Function>::Cast(value);
		}
	}

	Local<Value> ToJSON(Isolate* isolate, Local<Context> context, char* string) {
		return v8::JSON::Parse(context, String::NewFromUtf8(isolate, string).ToLocalChecked()).ToLocalChecked();
	}

	struct ByteWork {
		uv_async_t request{};
		v8::Persistent<Function> callback;
		bool isListener;
		ByteWork(Isolate* isolate, Local<Function> callback, bool isListener): callback(isolate, callback), isListener(isListener) {
			request.data = this;
			request.close_cb = close_callback_func;
		}

		~ByteWork() {
			delete result;
			delete error;
		}

		template<typename F, typename... Ts>
		void Invoke(F func, Ts... args) {
			ENSURE_UV(uv_async_init(uv_default_loop(), &this->request, this->node_callback_func));
			func(args..., go_callback_func, reinterpret_cast<uintptr_t>(this));
		}

		void Dispose() {
			this->callback.Reset();
			uv_close((uv_handle_t*)&request, NULL);
		}

		char* error = nullptr;
		char* result = nullptr;
		size_t length = -1;
		static void go_callback_func(uintptr_t ctx, void* result, void* error, size_t length) {
			ByteWork* work = reinterpret_cast<ByteWork*>(ctx);
			assert((error == nullptr) ^ (result == nullptr)); // one of them
			if (result) {
				memcpy(work->result = new char[length + 1], result, length);
				work->result[length] = '\0';
			}
			else {
				memcpy(work->error = new char[length + 1], error, length);
				work->error[length] = '\0';
			}
			work->length = length;
			ENSURE_UV(uv_async_send(&work->request));
		}

		static void node_callback_func(uv_async_t* request) {
			ByteWork* work = static_cast<ByteWork*>(request->data);

			Isolate* isolate = Isolate::GetCurrent();
			v8::HandleScope handleScope(isolate);

			Local<Context> ctx = isolate->GetCurrentContext();

			if (!work->isListener) {
				Local<Value> argv[2]{
					work->error == nullptr ? static_cast<Local<Value>>(v8::Null(isolate)) : ToJSON(isolate, ctx, work->error),
					work->result == nullptr ? static_cast<Local<Value>>(v8::Null(isolate)) : ToJSON(isolate, ctx, work->result)
				};

				Local<Function>::New(isolate, work->callback)->Call(ctx, ctx->Global(), 2, argv);
				work->Dispose();
			} else {
				Local<Value> argv[1]{
					ToJSON(isolate, ctx, work->result)
				};

				Local<Function>::New(isolate, work->callback)->Call(ctx, ctx->Global(), 1, argv);
			}
		}

		static void close_callback_func(uv_handle_t* request) {
			ByteWork* work = static_cast<ByteWork*>(request->data);
			delete work;
		}

		// For test purpose
		uint64_t now = uv_hrtime();
		void update_and_print(int tag) {
			uint64_t next = uv_hrtime();
			uint64_t duration = next - now;
			std::cout << tag << ":" << duration << std::endl;
			now = uv_hrtime();
		}
	};

	void instantiate(const FunctionCallbackInfo<Value>& args) {
		assert(args.IsConstructCall());
		
		Isolate* isolate = args.GetIsolate();
		Local<Context> ctx = isolate->GetCurrentContext();

		auto uid = Convert<int64_t>(isolate, args[0]);
		auto psw = Convert<std::string>(isolate, args[1]);
		void* bot = reinterpret_cast<void*>(_login(uid, const_cast<char*>(psw.c_str())));
		
		assert(bot);

		const auto This = args.This();
		This->SetAlignedPointerInInternalField(0, bot);
	}

	void onPrivateMessage(const FunctionCallbackInfo<Value>& args) {
		Isolate* isolate = args.GetIsolate();
		Local<Context> ctx = isolate->GetCurrentContext();
		const auto This = args.This();
		void* bot = This->GetAlignedPointerFromInternalField(0);

		auto callback = Convert<Local<Function>>(isolate, args[0]);
		ByteWork* work = new ByteWork(isolate, callback, true);
		work->Invoke(_onPrivateMessage, bot);

		args.GetReturnValue().Set(Undefined(isolate));
	}

	void init(Local<Object> exports, Local<Value> module, Local<Context> context) {
		Isolate* isolate = context->GetIsolate();
		// AddonContext* addon = new AddonContext(isolate);
		// Local<External> external = External::New(isolate, addon);

		auto t = FunctionTemplate::New(isolate, instantiate);
		auto ClientString = String::NewFromUtf8(isolate, "Client").ToLocalChecked();
		t->SetClassName(ClientString);

		auto inst_t = t->InstanceTemplate();
		inst_t->SetInternalFieldCount(1);

		auto proto_t = t->PrototypeTemplate();
		proto_t->Set(v8::Symbol::GetToStringTag(isolate), ClientString, static_cast<v8::PropertyAttribute>(v8::ReadOnly | v8::DontEnum | v8::DontDelete));
		proto_t->Set(isolate, "onPrivateMessage", v8::FunctionTemplate::New(isolate, onPrivateMessage));

		exports->Set(
			context,
			ClientString,
			t->GetFunction(context).ToLocalChecked()
		);
	}
}

NODE_MODULE_INIT(/*exports, module, context*/) {
	ATRI::init(exports, module, context);
}
