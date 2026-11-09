import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import type {
  ServerSettings,
  ServiceChangeTarget,
  SettingsSection,
} from "./settings-client";

type ServiceDraft = {
  prepareCancel: (
    target: ServiceChangeTarget,
  ) => (settings: ServerSettings) => void;
};

type PendingSave = {
  id: string;
  section: SettingsSection;
  title: string;
  label: string;
  save: () => void;
  cancel: () => void;
};
type SaveRegistration = PendingSave & { dirty: boolean };
const PendingSettingsContext = createContext<{
  saves: PendingSave[];
  register: (save: PendingSave) => () => void;
  registerServiceDraft: (draft: ServiceDraft) => () => void;
  prepareServiceCancel: ServiceDraft["prepareCancel"];
  reveal: (section: SettingsSection) => void;
}>({
  saves: [],
  register: () => () => {},
  registerServiceDraft: () => () => {},
  prepareServiceCancel: () => () => {},
  reveal: () => {},
});

export function PendingSettingsProvider({
  children,
  reveal,
}: {
  children: ReactNode;
  reveal: (section: SettingsSection) => void;
}) {
  const [saves, setSaves] = useState<PendingSave[]>([]);
  const serviceDrafts = useRef(new Set<ServiceDraft>());
  const registerServiceDraft = useCallback((draft: ServiceDraft) => {
    serviceDrafts.current.add(draft);
    return () => {
      serviceDrafts.current.delete(draft);
    };
  }, []);
  const prepareServiceCancel = useCallback((target: ServiceChangeTarget) => {
    const resets = [...serviceDrafts.current].map((draft) =>
      draft.prepareCancel(target),
    );
    return (settings: ServerSettings) =>
      resets.forEach((reset) => reset(settings));
  }, []);
  const register = useCallback((save: PendingSave) => {
    setSaves((current) => [
      ...current.filter((item) => item.id !== save.id),
      save,
    ]);
    return () => setSaves((current) => current.filter((item) => item !== save));
  }, []);
  const value = useMemo(
    () => ({
      saves,
      register,
      registerServiceDraft,
      prepareServiceCancel,
      reveal,
    }),
    [saves, register, registerServiceDraft, prepareServiceCancel, reveal],
  );
  return (
    <PendingSettingsContext.Provider value={value}>
      {children}
    </PendingSettingsContext.Provider>
  );
}

export function usePendingSettings() {
  return useContext(PendingSettingsContext);
}

// Keep action callbacks current without re-registering on every render.
// A hidden page stays mounted, so its draft and bottom actions remain available.
export function usePendingSettingsSave({
  id,
  section,
  title,
  label,
  dirty,
  save,
  cancel,
}: SaveRegistration) {
  const { register } = usePendingSettings();
  const callbacks = useRef({ save, cancel });
  callbacks.current = { save, cancel };
  useEffect(() => {
    if (!dirty) return;
    return register({
      id,
      section,
      title,
      label,
      save: () => callbacks.current.save(),
      cancel: () => callbacks.current.cancel(),
    });
  }, [register, id, section, title, label, dirty]);
}
