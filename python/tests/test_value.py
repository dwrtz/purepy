"""Runtime parity and defensive checks for the sole PurePy decorator."""

import dataclasses
import sys
import unittest

from purepy import value


@value
class User:
    identifier: int
    name: str


@value
class Group:
    users: tuple[User, ...]
    title: str | None


class ValueTests(unittest.TestCase):
    def test_positional_keyword_fields_and_equality(self):
        first = User(1, "Ada")
        self.assertEqual(first, User(name="Ada", identifier=1))
        self.assertEqual(first.identifier, 1)
        self.assertEqual(first.name, "Ada")
        self.assertNotEqual(first, User(2, "Ada"))
        self.assertEqual(tuple(field.name for field in dataclasses.fields(User)), ("identifier", "name"))

    def test_nominal_equality(self):
        @value
        class Other:
            identifier: int
            name: str
        self.assertNotEqual(User(1, "Ada"), Other(1, "Ada"))

    def test_frozen_slots(self):
        user = User(1, "Ada")
        self.assertFalse(hasattr(user, "__dict__"))
        with self.assertRaises(dataclasses.FrozenInstanceError):
            user.name = "Grace"
        with self.assertRaises(dataclasses.FrozenInstanceError):
            del user.name
        with self.assertRaises((AttributeError, TypeError)):
            user.other = 2

    def test_deep_values(self):
        group = Group((User(1, "Ada"),), None)
        self.assertEqual(group, Group(users=(User(1, "Ada"),), title=None))
        with self.assertRaises(dataclasses.FrozenInstanceError):
            group.users[0].name = "Grace"
        with self.assertRaises(TypeError):
            group.users[0] = User(2, "Grace")

    def test_wrong_constructor_binding(self):
        for call in (lambda: User(1), lambda: User(1, "Ada", "extra"),
                     lambda: User(1, name="Ada", identifier=2),
                     lambda: User(1, label="Ada")):
            with self.assertRaises(TypeError):
                call()

    def test_no_inheritance(self):
        with self.assertRaises(TypeError):
            class Child(User):
                pass
        class Base:
            pass
        with self.assertRaises(TypeError):
            @value
            class Child(Base):
                identifier: int

    def test_reject_methods_defaults_constants_properties_nested_classes(self):
        for extra in ({"x": 1}, {"f": lambda self: 1}, {"CONSTANT": 1},
                      {"field": property(lambda self: 1)}, {"Nested": type("Nested", (), {})},
                      {"__init__": lambda self: None}, {"__slots__": ()}):
            with self.subTest(extra=extra), self.assertRaises(TypeError):
                value(type("Invalid", (), {"__annotations__": {"x": int}, **extra}))

    def test_external_value_contract_is_owned_by_manifest_and_host(self):
        @dataclasses.dataclass(frozen=True, slots=True)
        class ExternalValue:
            text: str

        @value
        class ContainsExternal:
            external: ExternalValue

        self.assertEqual(ContainsExternal(ExternalValue("opaque")),
                         ContainsExternal(ExternalValue("opaque")))

    def test_opaque_value_construction_and_field_reads_do_not_dispatch(self):
        @dataclasses.dataclass(frozen=True, slots=True)
        class Opaque:
            number: int

            def __eq__(self, other):
                raise AssertionError("opaque equality must not be invoked")

            def __bool__(self):
                raise AssertionError("opaque truthiness must not be invoked")

            def __repr__(self):
                raise AssertionError("opaque formatting must not be invoked")

            def __hash__(self):
                raise AssertionError("opaque hashing must not be invoked")

        @value
        class Envelope:
            tokens: tuple[Opaque | None, ...]

        token = Opaque(7)
        envelope = Envelope(tokens=(token, None))
        self.assertIs(envelope.tokens[0], token)
        self.assertIsNone(envelope.tokens[1])
        with self.assertRaises(dataclasses.FrozenInstanceError):
            envelope.tokens = ()

    @unittest.skipIf(sys.version_info < (3, 14), "deferred annotation syntax requires Python 3.14")
    def test_forward_record_annotations_on_python314(self):
        # No source-level quoted annotations or future import is needed on 3.14.
        namespace = {"value": value, "__name__": __name__}
        exec("""
@value
class Outer:
    item: Later

@value
class Later:
    number: int
""", namespace)
        outer = namespace["Outer"](namespace["Later"](7))
        self.assertEqual(outer.item.number, 7)

    def test_primitive_optional_nested_tuple_fields(self):
        @value
        class Primitives:
            flag: bool
            number: float
            data: bytes
            nothing: None
            tuples: tuple[tuple[int, ...], ...]
        # Python evaluates an annotation spelled None as None, not NoneType.
        result = Primitives(True, 1.5, b"x", None, ((1, 2), ()))
        self.assertEqual(result.tuples, ((1, 2), ()))

    def test_no_custom_metaclass(self):
        class Meta(type):
            pass
        with self.assertRaises(TypeError):
            value(Meta("Invalid", (), {"__annotations__": {"x": int}}))

    def test_empty_record(self):
        @value
        class Empty:
            """An empty record is data-only."""
        self.assertEqual(Empty(), Empty())
        self.assertFalse(hasattr(Empty(), "__dict__"))


if __name__ == "__main__":
    unittest.main()
