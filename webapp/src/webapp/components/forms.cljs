(ns webapp.components.forms
  (:require
   ["@heroicons/react/24/outline" :as hero-outline-icon]
   ["@radix-ui/themes" :refer [IconButton Select TextArea TextField]]
   ["lucide-react" :refer [Eye EyeOff]]
   [clojure.string :as cs]
   [reagent.core :as r]))

(defn- form-label
  [text]
  [:label {:class "block text-xs font-semibold text-gray-800"} text])

(defn- form-label-dark
  [text]
  [:label {:class "text-white block text-xs font-semibold"} text])

(defn- form-helper-text
  [text dark]
  [:div {:class (str "relative flex flex-col group"
                     (when dark " text-white"))}
   [:> hero-outline-icon/QuestionMarkCircleIcon {:class "w-4 h-4"
                                                 :aria-hidden "true"}]
   [:div {:class "absolute bottom-0 flex-col hidden mb-6 w-max group-hover:flex"}
    [:span {:class (str "relative border -left-3 border-gray-300 bg-white rounded-md z-50 "
                        "p-2 text-xs text-gray-700 leading-none whitespace-no-wrap shadow-lg")}
     text]
    [:div {:class "w-3 h-3 -mt-2 border-r border-b border-gray-300 bg-white transform rotate-45"}]]])

(defonce common-styles
  "relative block w-full
   py-3 px-2
   border border-gray-300 rounded-md
   focus:ring-indigo-500 focus:border-indigo-500 sm:text-sm disabled:opacity-50")

(defonce common-styles-dark
  "relative block w-full bg-transparent
   py-3 px-2 text-white
   border border-gray-400 rounded-md
   focus:ring-white focus:border-white sm:text-sm disabled:opacity-50")

(defonce input-styles
  (str common-styles " h-12"))

(defonce input-styles-dark
  (str common-styles-dark " h-12"))

(defn input
  "Multi purpose HTML input component.
  Props signature:
  :label -> html label text;
  :placeholder -> html prop placeholder for input;
  :value -> a reagent atom piece of state."
  [_]
  (let [eye-open? (r/atom true)]
    (fn [{:keys [label
                 placeholder
                 name
                 dark
                 id
                 helper-text
                 value
                 defaultValue
                 on-change
                 on-keyDown
                 on-blur
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
                     :on-click #(swap! eye-open? not)}
            (if @eye-open?
              [:> Eye {:size 16}]
              [:> EyeOff {:size 16}])]])]])))

(defn input-metadata [{:keys [label name id placeholder disabled required value on-change]}]
  [:div {:class "relative"}
   [:label {:htmlFor label
            :class "absolute -top-2 left-2 inline-block bg-white px-1 text-xs font-medium text-gray-900"}
    label]
   [:div {:class "rt-TextFieldRoot rt-r-size-1 rt-variant-surface dark"}
    [:input {:type "text"
             :name name
             :id id
             :class "rt-reset rt-TextFieldInput"
             :placeholder placeholder
             :disabled (or disabled false)
             :required (or required false)
             :on-change on-change
             :value value}]]])

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
                        "rt-r-size-2"))}
    [:textarea
     {:class (str "rt-reset rt-TextAreaInput"
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
   [:> Select.Root {:size (or size "3")
                    :name name
                    :value selected
                    :default-value default-value
                    :on-value-change on-change
                    :required (or required false)
                    :disabled (or disabled false)}
    [:> Select.Trigger {:placeholder (or placeholder "Select one")
                        :variant (or variant "surface")
                        :class (str (when full-width? "w-full ")
                                    (when dark "dark"))}]
    [:> Select.Content {:position "popper"}
     (map #(option % selected) options)]]])

(defn select-editor
  "HTML select.
  Props signature:
  label -> html label text;
  options -> List of {:text string :value string};
  active -> the option value of an already active item;
  on-change -> function to be executed on change;
  required -> HTML required attribute;"
  [{:keys [name size options selected on-change full-width? required disabled]}]
  [:div {:class "text-xs"}
   [:> Select.Root {:size (or size "1")
                    :name name
                    :value selected
                    :on-value-change on-change
                    :required (or required false)
                    :disabled (or disabled false)}
    [:> Select.Trigger {:class (str "dark"
                                    (when full-width? "w-full"))
                        :placeholder "Select one"}]
    [:> Select.Content {:position "popper"}
     (map #(option % selected) options)]]])
