(ns webapp.components.forms
  (:require
   ["@radix-ui/themes" :refer [Select Tooltip Text TextField Box Flex]]
   ["lucide-react" :refer [Eye EyeOff HelpCircle]]
   [clojure.string :as cs]
   [reagent.core :as r]))

(defn- form-label
  [text]
  [:> Text {:size "1" :as "label" :weight "bold" :class "text-gray-12"}
   text])

(defn- form-label-dark
  [text]
  [:> Text {:size "1" :as "label" :weight "bold" :class "text-gray-12"}
   text])

(defn- form-helper-text
  [text]
  [:> Tooltip {:content text}
   [:> HelpCircle {:size 14}]])

(defn input
  "Multi purpose HTML input component.
  Props signature:
  :label -> html label text;
  :placeholder -> html prop placeholder for input;
  :value -> a reagent atom piece of state."
  [_]
  (let [eye-open? (r/atom true)
        toggle-eye #(swap! eye-open? not)]
    (fn [{:keys [label
                 placeholder
                 name
                 dark
                 id
                 helper-text
                 value
                 defaultValue
                 on-change
                 on-blur
                 on-keyDown
                 required
                 full-width?
                 pattern
                 disabled
                 minlength
                 maxlength
                 type
                 min
                 max
                 step
                 size
                 not-margin-bottom? ;; TODO: Remove this prop when remove margin-bottom from all inputs
                 hidden]}]
      [:div {:class (str "text-sm"
                         (when-not not-margin-bottom? " mb-regular")
                         (when full-width? " w-full")
                         (when hidden " hidden"))}
       [:div {:class "flex items-center gap-2 mb-1"}
        (when label
          (if dark
            [form-label-dark label]
            [form-label label]))
        (when (not (cs/blank? helper-text))
          [form-helper-text helper-text dark])]

       [:div {:class (str "rt-TextFieldRoot rt-variant-surface "
                          (case size
                            "3" "rt-r-size-3 "
                            "2" "rt-r-size-2 "
                            "1" "rt-r-size-1 "
                            "rt-r-size-3 ")
                          (when (= type "datetime-local") "*:block")
                          (when dark "dark"))}
        [:input
         {:type (if (= type "password")
                  (if @eye-open? "password" "text")
                  (or type "text"))
          :class "rt-reset rt-TextFieldInput"
          :id id
          :placeholder (or placeholder label)
          :name name
          :pattern pattern
          :minLength minlength
          :maxLength maxlength
          :min min
          :max max
          :step step
          :value value
          :defaultValue defaultValue
          :on-blur on-blur
          :on-change on-change
          :on-keyDown on-keyDown
          :disabled (or disabled false)
          :required (or required false)}]

        (when (= type "password")
          [:div {:data-side "right" :class "rt-TextFieldSlot"}
           [:button {:data-accent-color ""
                     :type "button"
                     :class (str "rt-reset rt-BaseButton rt-r-size-2 rt-variant-ghost rt-IconButton "
                                 (case size
                                   "3" "rt-r-size-2"
                                   "2" "rt-r-size-1"
                                   "rt-r-size-2"))
                     :on-click toggle-eye}
            (if @eye-open?
              [:> Eye {:size 16}]
              [:> EyeOff {:size 16}])]])]])))

(defn input-with-adornment
  "Input component with support for start-adornment (e.g., select dropdown inside input).
  Uses Radix UI TextField.Root for proper slot support.
  Props signature:
  :label -> html label text;
  :placeholder -> html prop placeholder for input;
  :value -> input value;
  :type -> input type (default: 'text');
  :required -> HTML required attribute;
  :on-change -> function to be executed on change;
  :helper-text -> optional helper text shown as tooltip;
  :start-adornment -> component to render as left adornment (e.g., select dropdown);
  :show-password? -> if true, password is visible initially (default: false, password hidden)."
  [{:keys [show-password?]}]
  (let [password-visible? (r/atom (boolean show-password?))
        toggle-password #(swap! password-visible? not)]
    (fn [{:keys [label
                 placeholder
                 helper-text
                 value
                 on-change
                 required
                 type
                 start-adornment]}]
      [:> Box {:class "text-sm mb-regular"}
       [:> Flex {:align "center" :gap "2" :class "mb-1"}
        (when label
          [form-label label])
        (when (not (cs/blank? helper-text))
          [form-helper-text helper-text])]

       [:> TextField.Root {:variant "surface"
                           :size "3"
                           :type (if (= type "password")
                                   (if @password-visible? "text" "password")
                                   (or type "text"))
                           :placeholder (or placeholder label)
                           :value value
                           :onChange on-change
                           :required (or required false)}
        (when start-adornment
          [:> TextField.Slot {:side "left"}
           start-adornment])
        (when (= type "password")
          [:> TextField.Slot {:side "right"}
           [:button {:data-accent-color ""
                     :type "button"
                     :class "rt-reset rt-BaseButton rt-r-size-2 rt-variant-ghost rt-IconButton rt-r-size-2"
                     :on-click toggle-password}
            (if @password-visible?
              [:> EyeOff {:size 16}]
              [:> Eye {:size 16}])]])]])))

(defn textarea
  [{:keys [label
           placeholder
           name
           dark
           size
           id
           helper-text
           value
           defaultValue
           on-change
           on-keyDown
           required
           on-blur
           rows
           autoFocus
           not-margin-bottom? ;; TODO: Remove this prop when remove margin-bottom from all inputs
           disabled]}]
  [:div {:class (when-not not-margin-bottom? "mb-regular")}
   [:div {:class "flex items-center gap-2 mb-1"}
    (if dark
      [form-label-dark label]
      [form-label label])
    (when (not (cs/blank? helper-text))
      [form-helper-text helper-text dark])]
   [:div {:class (str "rt-TextAreaRoot rt-variant-surface "
                      (case size
                        "3" "rt-r-size-3"
                        "2" "rt-r-size-2"
                        "1" "rt-r-size-1"
                        "rt-r-size-3"))}
    [:textarea
     {:class (str "rt-reset rt-TextAreaInput "
                  (when dark "dark"))
      :id (or id "")
      :rows (or rows 5)
      :name (or name "")
      :value value
      :defaultValue defaultValue
      :autoFocus autoFocus
      :placeholder placeholder
      :on-change on-change
      :on-blur on-blur
      :on-keyDown on-keyDown
      :disabled (or disabled false)
      :required (or required false)}]]])

(defn- option
  [item _]
  ^{:key (:value item)}
  [:> Select.Item {:value (:value item)} (:text item)])

(defn select
  "HTML select.
  Props signature:
  label -> html label text;
  options -> List of {:text string :value string};
  active -> the option value of an already active item;
  on-change -> function to be executed on change;
  required -> HTML required attribute;"
  [{:keys [label
           not-margin-bottom?
           variant
           helper-text
           name
           size
           options
           placeholder
           selected
           default-value
           on-change
           required
           disabled
           full-width?
           dark]}]
  [:div {:class (str " text-sm w-full"
                     (when-not not-margin-bottom? " mb-regular"))}
   [:div {:class "flex items-center gap-2 mb-1"}
    (if dark
      [form-label-dark label]
      [form-label label])
    (when (not (cs/blank? helper-text))
      [form-helper-text helper-text dark])]
   [:> Select.Root (merge
                    (when-not (cs/blank? default-value)
                      {:default-value default-value})
                    (when-not (cs/blank? selected)
                      {:value selected})
                    {:size (or size "3")
                     :name name
                     :on-value-change on-change
                     :required (or required false)
                     :disabled (or disabled false)})
    [:> Select.Trigger {:placeholder (or placeholder "Select one")
                        :variant (or variant "surface")
                        :color "indigo"
                        :class (str (when full-width? "w-full ")
                                    (when dark "dark"))}]
    [:> Select.Content {:position "popper"
                        :color "indigo"}
     (map #(option % selected) options)]]])
