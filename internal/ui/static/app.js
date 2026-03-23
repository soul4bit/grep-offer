document.addEventListener("DOMContentLoaded", function () {
  var deployLocked = document.body.dataset.deployLocked === "true";
  var deployBanner = document.querySelector("[data-deploy-lock-banner]");

  if (deployLocked) {
    document.querySelectorAll("form").forEach(function (form) {
      var method = (form.getAttribute("method") || "get").toLowerCase();
      if (method === "get") {
        return;
      }

      form.classList.add("is-deploy-locked");
      form.querySelectorAll("button, input, select, textarea").forEach(function (field) {
        if (field.type === "hidden") {
          return;
        }
        field.disabled = true;
      });

      form.addEventListener("submit", function (event) {
        event.preventDefault();
        if (deployBanner) {
          deployBanner.scrollIntoView({ behavior: "smooth", block: "center" });
        }
      });
    });
  }

  document.querySelectorAll(".anti-autofill-input").forEach(function (input) {
    var unlock = function () {
      input.removeAttribute("readonly");
    };

    input.addEventListener("pointerdown", unlock, { once: true });
    input.addEventListener("focus", unlock, { once: true });
    input.addEventListener("keydown", unlock, { once: true });
  });

  var validators = {
    username: {
      test: function (value) {
        return /^[a-zA-Z0-9._-]{3,24}$/.test(value);
      },
      idle: "3-24 символа, ASCII и без внезапного hostname from hell.",
      valid: "Ник выглядит нормально. Bash не плачет.",
      invalid: "Нужны 3-24 ASCII-символа: буквы, цифры, точка, дефис или _."
    },
    email: {
      test: function (value) {
        return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value);
      },
      idle: "Почта должна выглядеть как нормальная почта.",
      valid: "Почта выглядит так, будто письмо реально дойдет.",
      invalid: "Пока не похоже на валидный email."
    },
    password: {
      test: function (value) {
        return value.length >= 8;
      },
      idle: "Минимум 8 символов. Совсем уж не рофлим над безопасностью.",
      valid: "Пароль ок. Brute-force сегодня не улыбается.",
      invalid: "Нужно минимум 8 символов."
    },
    "confirm-password": {
      test: function (value, input, form) {
        var passwordInput = form.querySelector('input[name="password"]');
        return Boolean(passwordInput) && value.length >= 8 && value === passwordInput.value;
      },
      idle: "Должен совпасть. Иначе даже до письма дойдет драма.",
      valid: "Совпало. Прод пока не распался.",
      invalid: "Не совпало. Проверь еще раз."
    }
  };

  document.querySelectorAll("[data-auth-form]").forEach(function (form) {
    var bumpField = function (field) {
      field.classList.remove("is-shaking");
      void field.offsetWidth;
      field.classList.add("is-shaking");
    };

    var updateField = function (input) {
      var kind = input.dataset.validate;
      var field = input.closest(".field");
      var status = form.querySelector('[data-field-status="' + kind + '"]');
      var validator = validators[kind];
      if (!field || !status || !validator) {
        return true;
      }

      var value = input.value.trim();
      field.classList.remove("is-valid", "is-invalid", "is-idle");

      if (!value) {
        field.classList.add("is-idle");
        status.textContent = validator.idle;
        input.removeAttribute("aria-invalid");
        return false;
      }

      var valid = validator.test(value, input, form);
      field.classList.add(valid ? "is-valid" : "is-invalid");
      status.textContent = valid ? validator.valid : validator.invalid;
      input.setAttribute("aria-invalid", valid ? "false" : "true");
      return valid;
    };

    var validateForm = function () {
      var invalidInputs = [];

      form.querySelectorAll("[data-validate]").forEach(function (input) {
        var valid = updateField(input);
        if (!valid) {
          invalidInputs.push(input);
        }
      });

      return invalidInputs;
    };

    form.querySelectorAll("[data-validate]").forEach(function (input) {
      var sync = function () {
        updateField(input);

        if (input.name === "password") {
          var confirm = form.querySelector('[data-validate="confirm-password"]');
          if (confirm) {
            updateField(confirm);
          }
        }
      };

      sync();
      input.addEventListener("input", sync);
      input.addEventListener("blur", sync);
    });

    form.addEventListener("submit", function (event) {
      var invalidInputs = validateForm();
      if (!invalidInputs.length) {
        return;
      }

      event.preventDefault();
      invalidInputs.forEach(function (input) {
        var field = input.closest(".field");
        if (field) {
          bumpField(field);
        }
      });

      invalidInputs[0].focus();
    });
  });
});
